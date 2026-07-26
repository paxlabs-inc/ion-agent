package dreamweaver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/liveness/dreamweaver"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/activation"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
	"github.com/paxlabs-inc/ion-agent/internal/provider"
	"github.com/paxlabs-inc/ion-agent/internal/security/memoryguard"
)

type dreamCipher struct{}

func (dreamCipher) Encrypt(plaintext []byte) ([]byte, error) {
	return append([]byte(nil), plaintext...), nil
}

func (dreamCipher) Decrypt(ciphertext []byte) ([]byte, error) {
	return append([]byte(nil), ciphertext...), nil
}

type dreamClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *dreamClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

type guardAlerts struct{ count atomic.Int64 }

func (alerts *guardAlerts) ProtectedMemoryTargeted(memoryguard.Event) {
	alerts.count.Add(1)
}

type emptyPrefetcher struct{}

func (emptyPrefetcher) Prefetch(
	context.Context,
	activation.Request,
) (*activation.PrefetchResult, error) {
	return &activation.PrefetchResult{}, nil
}

func TestDreamweaverAcceptanceRealCortexProviderReorganizationAndActivation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)
	clock := &dreamClock{now: now}
	store, source := openDreamCortex(t, filepath.Join(t.TempDir(), "dream.journal"), clock)
	defer func() {
		_ = store.Close()
		_ = source.Close()
	}()

	protected := make(map[memory.Type]*cortex.Memory)
	for _, memoryType := range []memory.Type{
		memory.Identity,
		memory.Preference,
		memory.Constraint,
	} {
		stored, err := store.Write(
			ctx,
			memoryType,
			mustDreamJSON(t, map[string]any{
				"text": "binding " + string(memoryType), "domain": "identity",
			}),
			"user",
		)
		if err != nil {
			t.Fatal(err)
		}
		protected[memoryType] = stored
	}

	domains := []string{"deployment", "api", "security", "deployment", "api", "security"}
	sources := make([]uuid.UUID, len(domains))
	for index, domain := range domains {
		stored, err := store.WriteWithTrust(
			ctx,
			memory.Fact,
			mustDreamJSON(t, map[string]any{
				"text":   fmt.Sprintf("%s observation %d", domain, index+1),
				"domain": domain,
			}),
			"verified-observer",
			cortex.TrustHigh,
		)
		if err != nil {
			t.Fatal(err)
		}
		sources[index] = stored.Head.ID
	}

	var providerCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		providerCalls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		var requestObject map[string]json.RawMessage
		if err := json.Unmarshal(body, &requestObject); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if _, exists := requestObject["tools"]; exists {
			t.Error("Dreamweaver exposed tools to pattern provider")
		}
		var insights []dreamweaver.ProposedInsight
		for index := 0; index < dreamweaver.MaxBeliefsPerCycle+1; index++ {
			insights = append(insights, dreamweaver.ProposedInsight{
				Statement: fmt.Sprintf("cross-domain insight %d", index+1),
				SourceMemoryIDs: []uuid.UUID{
					sources[index%len(sources)],
					sources[(index+1)%len(sources)],
					sources[(index+2)%len(sources)],
				},
			})
		}
		// A malicious suggestion that cites a protected, non-candidate memory
		// must be discarded by the engine-side provenance validator.
		insights = append(insights, dreamweaver.ProposedInsight{
			Statement: "rewrite identity from a dream",
			SourceMemoryIDs: []uuid.UUID{
				protected[memory.Identity].Head.ID,
				sources[0],
				sources[1],
			},
		})
		content, _ := json.Marshal(map[string]any{"insights": insights})
		response := map[string]any{
			"model": "pattern-model",
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": string(content)},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2,
			},
		}
		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()
	pool := newDreamProviderPool(t, server)
	alerts := &guardAlerts{}
	guard, err := memoryguard.New(clock, alerts)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := dreamweaver.New(dreamweaver.Config{
		Store: store, Guard: guard,
		Generator: dreamweaver.ModelPatternGenerator{
			Provider: pool,
			Model:    "pattern-model",
		},
		Clock:       clock,
		ClusterSize: len(sources),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.RunCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if providerCalls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", providerCalls.Load())
	}
	if result.Quarantined ||
		len(result.Selected) != len(sources) ||
		result.Patterns != dreamweaver.MaxBeliefsPerCycle ||
		len(result.Beliefs) != dreamweaver.MaxBeliefsPerCycle ||
		len(result.Provenance) != dreamweaver.MaxBeliefsPerCycle ||
		len(result.Reorganized) != len(sources) {
		t.Fatalf("cycle result = %+v", result)
	}

	for _, link := range result.Provenance {
		if len(link.SourceMemoryIDs) < dreamweaver.MinSupportingMemories ||
			!containsDreamID(result.Beliefs, link.DerivedBeliefID) {
			t.Fatalf("invalid provenance link = %+v", link)
		}
		resolved, err := store.Resolve(link.DerivedBeliefID)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Head.Type != memory.Belief ||
			resolved.Head.SourceTrust != float64(cortex.TrustHigh) {
			t.Fatalf("derived memory metadata = %+v", resolved.Head)
		}
		var belief dreamweaver.DerivedBelief
		if err := json.Unmarshal(resolved.Version.Data, &belief); err != nil {
			t.Fatal(err)
		}
		if !belief.DreamDerived || !belief.Consolidated ||
			len(belief.SourceMemoryIDs) < dreamweaver.MinSupportingMemories ||
			len(belief.SourceDomains) < 2 {
			t.Fatalf("derived belief = %+v", belief)
		}
	}
	for _, id := range sources {
		resolved, err := store.Resolve(id)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Head.CurrentVersion != 2 {
			t.Fatalf("source %s version = %d, want graph reorganization", id, resolved.Head.CurrentVersion)
		}
		var reorganization struct {
			Connections       []uuid.UUID `json:"connections"`
			LastReorganizedAt *time.Time  `json:"last_reorganized_at"`
		}
		if err := json.Unmarshal(resolved.Version.Data, &reorganization); err != nil {
			t.Fatal(err)
		}
		wantConnections := 0
		for _, link := range result.Provenance {
			if containsDreamID(link.SourceMemoryIDs, id) {
				wantConnections++
			}
		}
		if len(reorganization.Connections) != wantConnections ||
			reorganization.LastReorganizedAt == nil {
			t.Fatalf("source reorganization = %+v, want %d provenance links",
				reorganization, wantConnections)
		}
	}
	for memoryType, original := range protected {
		resolved, err := store.Resolve(original.Head.ID)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Head.CurrentVersion != 1 ||
			string(resolved.Version.Data) != string(original.Version.Data) {
			t.Fatalf("protected %s was modified: %+v", memoryType, resolved)
		}
	}
	if alerts.count.Load() != 0 {
		t.Fatalf("protected-memory alerts = %d; protected memories should never be targeted",
			alerts.count.Load())
	}

	dreams, err := activation.NewDreamBeliefs(store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := activation.NewService(32000, emptyPrefetcher{}, dreams)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := service.Activate(ctx, "current task", "user", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "## dreams") ||
		!strings.Contains(rendered, "dream-derived belief") ||
		!strings.Contains(rendered, "cross-domain insight") {
		t.Fatalf("Dreams activation tier = %q", rendered)
	}
}

func TestDreamweaverAcceptanceAdversarialSourceAutoQuarantines(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 19, 16, 0, 0, 0, time.UTC)
	clock := &dreamClock{now: now}
	store, source := openDreamCortex(t, filepath.Join(t.TempDir(), "quarantine.journal"), clock)
	defer func() {
		_ = store.Close()
		_ = source.Close()
	}()
	for index, domain := range []string{"api", "deployment", "security"} {
		trust := cortex.TrustHigh
		if index == 1 {
			trust = cortex.TrustUntrusted
		}
		if _, err := store.WriteWithTrust(
			ctx,
			memory.Fact,
			mustDreamJSON(t, map[string]any{
				"text": fmt.Sprintf("source %d", index), "domain": domain,
			}),
			"observer",
			trust,
		); err != nil {
			t.Fatal(err)
		}
	}

	var providerCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	guard, err := memoryguard.New(clock, &guardAlerts{})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := dreamweaver.New(dreamweaver.Config{
		Store: store, Guard: guard,
		Generator: dreamweaver.ModelPatternGenerator{
			Provider: newDreamProviderPool(t, server),
			Model:    "pattern-model",
		},
		Clock: clock, ClusterSize: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.RunCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Quarantined ||
		len(result.Beliefs) != 0 ||
		!strings.Contains(result.Reason, "adversarial") {
		t.Fatalf("quarantine result = %+v", result)
	}
	if providerCalls.Load() != 0 {
		t.Fatalf("provider called %d times after adversarial source detection",
			providerCalls.Load())
	}
	if ids := store.ListByType(memory.Belief); len(ids) != 0 {
		t.Fatalf("beliefs written from adversarial cluster = %v", ids)
	}
	events := store.ListByType(memory.Event)
	if len(events) != 1 {
		t.Fatalf("quarantine events = %v", events)
	}
	resolved, err := store.Resolve(events[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resolved.Version.Data), "dreamweaver_quarantine") {
		t.Fatalf("quarantine event = %s", resolved.Version.Data)
	}
}

func openDreamCortex(
	t *testing.T,
	path string,
	clock *dreamClock,
) (*cortex.Cortex, *journal.Journal) {
	t.Helper()
	source, err := journal.Open(path, dreamCipher{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := cortex.New(cortex.Config{
		Actor: "dreamweaver-acceptance", Journal: source, Clock: clock,
	})
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	return store, source
}

func newDreamProviderPool(t *testing.T, server *httptest.Server) *provider.Pool {
	t.Helper()
	pool, err := provider.NewPool([]provider.Endpoint{{
		Name: "dream-patterns", URL: server.URL, Model: "pattern-model",
		Adapter: provider.OpenAIAdapter{},
		Credentials: []provider.Credential{{
			ID: "acceptance", Secret: "secret",
		}},
		Authentication: provider.BearerAuthentication(),
		Client:         server.Client(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func mustDreamJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func containsDreamID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
