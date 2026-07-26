package curiosity_test

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

	"github.com/paxlabs-inc/ion-agent/internal/liveness/curiosity"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
	"github.com/paxlabs-inc/ion-agent/internal/presence/automatrix"
	"github.com/paxlabs-inc/ion-agent/internal/provider"
)

type acceptanceCipher struct{}

func (acceptanceCipher) Encrypt(plaintext []byte) ([]byte, error) {
	return append([]byte(nil), plaintext...), nil
}

func (acceptanceCipher) Decrypt(ciphertext []byte) ([]byte, error) {
	return append([]byte(nil), ciphertext...), nil
}

type acceptanceClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *acceptanceClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func TestCuriosityAcceptanceRealCortexProviderAndAutomatrix(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	clock := &acceptanceClock{now: now}
	journalPath := filepath.Join(t.TempDir(), "curiosity.journal")
	store, source := openAcceptanceCortex(t, journalPath, clock)

	texts := []string{
		"orphaned deployment observation",
		"the service endpoint requires port 443",
		"the service endpoint requires port 80 only",
		"deployment retries use exponential backoff",
		"the build uses locked dependencies",
		"health probes are read only",
		"session state is encrypted",
		"the journal is append only",
		"tool calls cross the policy pipeline",
		"provider requests use normalized O1 messages",
	}
	ids := make([]uuid.UUID, len(texts))
	for index, text := range texts {
		payload := mustJSON(t, curiosity.GraphDocument{Text: text, AccessCount: index + 1})
		stored, err := store.Write(ctx, memory.Fact, payload, "acceptance")
		if err != nil {
			t.Fatal(err)
		}
		ids[index] = stored.Head.ID
	}
	// Leave node zero isolated and make nodes one through nine a dense region.
	for index := 1; index < len(ids); index++ {
		var connections []uuid.UUID
		for candidate := 1; candidate < len(ids); candidate++ {
			if candidate != index {
				connections = append(connections, ids[candidate])
			}
		}
		payload := mustJSON(t, curiosity.GraphDocument{
			Text:        texts[index],
			Connections: connections,
			AccessCount: index + 1,
		})
		if _, err := store.Update(ctx, ids[index], payload, "acceptance"); err != nil {
			t.Fatal(err)
		}
	}

	serverCalls := atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		serverCalls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read provider request: %v", err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		var requestObject map[string]json.RawMessage
		if err := json.Unmarshal(body, &requestObject); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		if _, exists := requestObject["tools"]; exists {
			t.Error("entailment request exposed a tool surface")
		}
		score := 0.9
		bodyText := string(body)
		if strings.Contains(bodyText, "port 443") &&
			strings.Contains(bodyText, "port 80 only") {
			score = 0.1
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{
			"model":"entailment-model",
			"choices":[{"message":{"content":"{\"score\":%.1f}"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`, score)
	}))
	defer server.Close()
	pool, err := provider.NewPool([]provider.Endpoint{{
		Name:           "entailment",
		URL:            server.URL,
		Model:          "entailment-model",
		Adapter:        provider.OpenAIAdapter{},
		Credentials:    []provider.Credential{{ID: "acceptance", Secret: "secret"}},
		Authentication: provider.BearerAuthentication(),
		Client:         server.Client(),
	}})
	if err != nil {
		t.Fatal(err)
	}

	queue := automatrix.NewQueue()
	engine, err := curiosity.New(curiosity.Config{
		Store:  store,
		Queue:  queue,
		Scorer: curiosity.ModelEntailmentScorer{Provider: pool, Model: "entailment-model"},
		Clock:  clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	for occurrence := 0; occurrence < curiosity.GapOccurrenceThreshold; occurrence++ {
		if _, err := engine.RecordGap(
			ctx,
			"How is the legacy token refreshed?",
			fmt.Sprintf("attempt %d", occurrence+1),
			fmt.Sprintf("session-%d", occurrence+1),
			[]uuid.UUID{ids[1]},
		); err != nil {
			t.Fatal(err)
		}
	}
	for occurrence := 0; occurrence < curiosity.GapOccurrenceThreshold-1; occurrence++ {
		if _, err := engine.RecordGap(
			ctx,
			"Which optional formatter is installed?",
			"",
			fmt.Sprintf("other-%d", occurrence),
			nil,
		); err != nil {
			t.Fatal(err)
		}
	}

	// Reopen the journal-authoritative graph before scanning. Detection must not
	// rely on process-local observations.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	store, source = openAcceptanceCortex(t, journalPath, clock)
	defer func() {
		_ = store.Close()
		_ = source.Close()
	}()
	engine, err = curiosity.New(curiosity.Config{
		Store:  store,
		Queue:  queue,
		Scorer: curiosity.ModelEntailmentScorer{Provider: pool, Model: "entailment-model"},
		Clock:  clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	targets, err := engine.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if serverCalls.Load() != 45 {
		t.Fatalf("pairwise provider calls = %d, want 45", serverCalls.Load())
	}
	if len(targets) != 3 {
		t.Fatalf("targets = %+v, want exactly three anomaly classes", targets)
	}

	byType := make(map[curiosity.TargetType]curiosity.Target)
	for _, target := range targets {
		byType[target.Type] = target
		wantPriority := target.Factors.Recency *
			target.Factors.Frequency *
			target.Factors.GraphCentrality
		if target.Priority != wantPriority {
			t.Fatalf("priority = %v, want recency x frequency x centrality = %v",
				target.Priority, wantPriority)
		}
	}
	if fringe := byType[curiosity.TargetFringe]; len(fringe.SourceMemoryIDs) != 1 ||
		fringe.SourceMemoryIDs[0] != ids[0] {
		t.Fatalf("fringe target = %+v, want isolated Cortex node", fringe)
	}
	if contradiction := byType[curiosity.TargetContradiction]; len(contradiction.SourceMemoryIDs) != 2 ||
		!containsUUID(contradiction.SourceMemoryIDs, ids[1]) ||
		!containsUUID(contradiction.SourceMemoryIDs, ids[2]) {
		t.Fatalf("contradiction target = %+v", contradiction)
	}
	if gap := byType[curiosity.TargetGap]; len(gap.SourceMemoryIDs) != 3 ||
		!strings.Contains(gap.Description, "legacy token") {
		t.Fatalf("gap target = %+v", gap)
	}
	queued := queue.Snapshot()
	if len(queued) != 3 {
		t.Fatalf("Automatrix queue = %+v, want three targets", queued)
	}
	for _, item := range queued {
		if item.Source != automatrix.SourceCuriosity || len(item.Payload) == 0 {
			t.Fatalf("queued curiosity work = %+v", item)
		}
	}
}

func openAcceptanceCortex(
	t *testing.T,
	path string,
	clock *acceptanceClock,
) (*cortex.Cortex, *journal.Journal) {
	t.Helper()
	source, err := journal.Open(path, acceptanceCipher{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := cortex.New(cortex.Config{
		Actor: "curiosity-acceptance", Journal: source, Clock: clock,
	})
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	return store, source
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func containsUUID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
