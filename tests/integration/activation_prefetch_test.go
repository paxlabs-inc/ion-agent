//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/belief/premise"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/activation"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/memory/hnsw"
	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type integrationCipher struct{}

func (integrationCipher) Encrypt(plaintext []byte) ([]byte, error) {
	return append([]byte(nil), plaintext...), nil
}

func (integrationCipher) Decrypt(ciphertext []byte) ([]byte, error) {
	return append([]byte(nil), ciphertext...), nil
}

type authorizedSessions struct {
	userID   string
	sessions []uuid.UUID
}

func (scope authorizedSessions) AuthorizedSessions(
	_ context.Context,
	userID string,
	current uuid.UUID,
) ([]uuid.UUID, error) {
	if userID != scope.userID {
		return nil, errors.New("unauthorized user")
	}
	for _, id := range scope.sessions {
		if id == current {
			return append([]uuid.UUID(nil), scope.sessions...), nil
		}
	}
	return nil, errors.New("unauthorized session")
}

type vectorContent map[uint64]string

func (content vectorContent) ResolveVectorKeys(
	_ context.Context,
	keys []uint64,
) ([]string, error) {
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		value, exists := content[key]
		if !exists {
			return nil, fmt.Errorf("unknown vector key %d", key)
		}
		values = append(values, value)
	}
	return values, nil
}

func TestActivationPrefetchRealSourcesP99AndSessionIsolation(t *testing.T) {
	binary := os.Getenv("ION_HNSW_BINARY")
	if binary == "" {
		binary = filepath.Join(
			"..", "..", "hnsw-service", "target", "debug", "ion-hnsw",
		)
	}
	absoluteBinary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(absoluteBinary); errors.Is(err, os.ErrNotExist) {
		t.Skip("Rust HNSW binary is not built")
	} else if err != nil {
		t.Fatal(err)
	}

	const dimensions = 64
	ctx := context.Background()
	socket := filepath.Join(t.TempDir(), "activation-hnsw.sock")
	process := startHNSWProcess(t, absoluteBinary, socket, dimensions)
	defer process.crash(t)
	client, err := hnsw.NewClient(socket, dimensions, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	vectorStore, err := hnsw.OpenSQLiteStore(
		ctx,
		filepath.Join(t.TempDir(), "activation-vectors.db"),
		dimensions,
	)
	if err != nil {
		t.Fatal(err)
	}
	index, err := hnsw.NewIndex(ctx, hnsw.Config{
		Remote: client, Store: vectorStore, Dimensions: dimensions,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	embedder, err := hnsw.NewHashEmbedder(dimensions)
	if err != nil {
		t.Fatal(err)
	}
	resolver := vectorContent{}
	for key := uint64(1); key <= 100; key++ {
		content := fmt.Sprintf(
			"deployment memory %d uses a durable encrypted journal",
			key,
		)
		resolver[key] = content
		vector, embedErr := embedder.Embed(ctx, content)
		if embedErr != nil {
			t.Fatal(embedErr)
		}
		if err := index.Upsert(ctx, key, vector); err != nil {
			t.Fatal(err)
		}
	}
	semantic, err := activation.NewSemanticAdapter(embedder, index, resolver)
	if err != nil {
		t.Fatal(err)
	}

	sessionStore, err := session.Open(
		ctx,
		filepath.Join(t.TempDir(), "sessions.db"),
		integrationCipher{},
		types.SystemClock{},
		32000,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionStore.Close(ctx)
	current, err := sessionStore.CreateSession(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := sessionStore.CreateSession(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized, err := sessionStore.CreateSession(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for id, content := range map[uuid.UUID]string{
		current.ID:      "deployment durable journal transcript belongs to current session",
		authorized.ID:   "deployment durable journal timeline is authorized history",
		unauthorized.ID: "deployment durable journal secret belongs to another user",
	} {
		if _, err := sessionStore.AppendMessage(
			ctx,
			id,
			session.RoleUser,
			session.MemoryTranscript,
			[]byte(content),
			10,
		); err != nil {
			t.Fatal(err)
		}
	}
	fts, err := activation.NewSessionFTSAdapter(
		sessionStore,
		authorizedSessions{
			userID: "user-a", sessions: []uuid.UUID{current.ID, authorized.ID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	sourceJournal, err := journal.Open(
		filepath.Join(t.TempDir(), "cortex.journal"),
		integrationCipher{},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceJournal.Close()
	cortexStore, err := cortex.New(cortex.Config{
		Actor: "user-a", Journal: sourceJournal, Clock: types.SystemClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cortexStore.Close()
	for index := 0; index < 20; index++ {
		content := []byte(fmt.Sprintf(
			`{"fact":"salient deployment memory %d"}`,
			index,
		))
		if _, err := cortexStore.Write(
			ctx,
			memory.Fact,
			content,
			"integration",
		); err != nil {
			t.Fatal(err)
		}
	}
	salience, err := activation.NewCortexSalience(
		cortexStore,
		types.SystemClock{},
	)
	if err != nil {
		t.Fatal(err)
	}

	ledger, err := premise.New(types.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Add(
		"deployments require an authenticated receipt",
		premise.SourceUser,
		0,
	); err != nil {
		t.Fatal(err)
	}
	premises, err := activation.NewLedgerPremises(ledger)
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := activation.NewPipeline(semantic, fts, salience, premises)
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := activation.NewSessionTranscript(sessionStore)
	if err != nil {
		t.Fatal(err)
	}
	service, err := activation.NewService(32000, pipeline, transcript)
	if err != nil {
		t.Fatal(err)
	}

	const query = "deployment durable journal"
	rendered, err := service.Activate(
		ctx,
		query,
		"user-a",
		current.ID.String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"authenticated receipt",
		"authorized history",
		"durable encrypted journal",
		"salient deployment memory",
		"belongs to current session",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("activation omitted %q:\n%s", expected, rendered)
		}
	}
	if strings.Contains(rendered, "another user") {
		t.Fatalf("activation leaked an unauthorized session:\n%s", rendered)
	}

	const samples = 200
	durations := make([]time.Duration, samples)
	for sample := range durations {
		started := time.Now()
		if _, err := service.Activate(
			ctx,
			query,
			"user-a",
			current.ID.String(),
		); err != nil {
			t.Fatal(err)
		}
		durations[sample] = time.Since(started)
	}
	sort.Slice(durations, func(left, right int) bool {
		return durations[left] < durations[right]
	})
	p99 := durations[(samples*99+99)/100-1]
	if p99 >= 30*time.Millisecond {
		t.Fatalf("activation prefetch p99 %s exceeds 30ms", p99)
	}
	t.Logf("activation prefetch p99: %s", p99)
}
