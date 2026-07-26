package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	memoryjournal "github.com/paxlabs-inc/ion-agent/internal/memory/journal"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func TestMemoryRecoverSelectsArchivedVersionAndPersistsWithoutMutatingSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	journalPath := filepath.Join(root, "cortex", "journal.bin")
	cipher, err := vault.New(bytes.Repeat([]byte{0x6d}, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	openStore := func() (*cortex.Cortex, *memoryjournal.Journal) {
		t.Helper()
		journal, openErr := memoryjournal.Open(journalPath, cipher)
		if openErr != nil {
			t.Fatal(openErr)
		}
		store, openErr := cortex.New(cortex.Config{
			Actor: "operator", Journal: journal, Clock: types.SystemClock{},
		})
		if openErr != nil {
			_ = journal.Close()
			t.Fatal(openErr)
		}
		return store, journal
	}
	store, journal := openStore()
	actorID := uuid.New()
	otherActor := uuid.New()
	source, err := store.WriteForActor(
		context.Background(),
		actorID.String(),
		memory.Fact,
		[]byte(`{"content":"first","pinned":false}`),
		"test",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateForActor(
		context.Background(),
		actorID.String(),
		source.Head.ID,
		[]byte(`{"content":"second","pinned":true}`),
		"test",
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Tombstone(
		context.Background(), source.Head.ID, "test archive", "test",
	); err != nil {
		t.Fatal(err)
	}
	live, err := store.WriteForActor(
		context.Background(),
		actorID.String(),
		memory.Preference,
		[]byte(`{"content":"live","pinned":false}`),
		"test",
	)
	if err != nil {
		t.Fatal(err)
	}

	manager, err := tools.NewManager(
		types.SystemClock{}, tools.WithExecutionPolicy(allowPolicy{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(context.Background(), manager, Config{
		Workspace: root,
		Memory:    store,
	}); err != nil {
		t.Fatal(err)
	}
	execute := func(
		ctx context.Context,
		callID string,
		id uuid.UUID,
		version uint64,
	) (json.RawMessage, error) {
		t.Helper()
		arguments, marshalErr := json.Marshal(map[string]any{
			"id": id, "version": version,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return manager.Execute(ctx, protocol.NormalizedToolCall{
			ID: callID, Name: "memory_recover", Arguments: arguments,
		})
	}
	actorContext := controlplane.WithApprovalScope(
		context.Background(),
		controlplane.ApprovalScope{ActorID: actorID},
	)
	result, err := execute(actorContext, "recover-v1", source.Head.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	var recoveredResult struct {
		SourceID      uuid.UUID `json:"source_id"`
		SourceVersion uint64    `json:"source_version"`
		RecoveredID   uuid.UUID `json:"recovered_id"`
	}
	if err := json.Unmarshal(result, &recoveredResult); err != nil {
		t.Fatal(err)
	}
	if recoveredResult.SourceID != source.Head.ID ||
		recoveredResult.SourceVersion != 1 ||
		recoveredResult.RecoveredID == uuid.Nil ||
		recoveredResult.RecoveredID == source.Head.ID {
		t.Fatalf("recovery result = %+v", recoveredResult)
	}
	recovered, err := store.ResolveForActor(
		actorID.String(), recoveredResult.RecoveredID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(recovered.Version.Data, []byte(`"first"`)) ||
		bytes.Contains(recovered.Version.Data, []byte(`"second"`)) {
		t.Fatalf("recovered data = %s", recovered.Version.Data)
	}
	archived, err := store.ResolveVersionForActor(
		actorID.String(), source.Head.ID, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Head.Tombstoned == nil ||
		archived.Head.CurrentVersion != 2 ||
		!bytes.Contains(archived.Version.Data, []byte(`"second"`)) {
		t.Fatalf("archived source changed = %+v %s", archived.Head, archived.Version.Data)
	}
	otherContext := controlplane.WithApprovalScope(
		context.Background(),
		controlplane.ApprovalScope{ActorID: otherActor},
	)
	if _, err := execute(
		otherContext, "cross-actor", source.Head.ID, 1,
	); !errors.Is(err, cortex.ErrNotFound) {
		t.Fatalf("cross-actor recovery error = %v", err)
	}
	if _, err := execute(
		actorContext, "recover-live", live.Head.ID, 1,
	); err == nil || !strings.Contains(err.Error(), "not archived") {
		t.Fatalf("live recovery error = %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	store, journal = openStore()
	defer journal.Close()
	defer store.Close()
	archived, err = store.ResolveVersionForActor(
		actorID.String(), source.Head.ID, 0,
	)
	if err != nil || archived.Head.Tombstoned == nil ||
		archived.Head.CurrentVersion != 2 {
		t.Fatalf("replayed source = %+v, %v", archived, err)
	}
	recovered, err = store.ResolveForActor(
		actorID.String(), recoveredResult.RecoveredID,
	)
	if err != nil || !bytes.Contains(recovered.Version.Data, []byte(`"first"`)) {
		t.Fatalf("replayed recovery = %+v, %v", recovered, err)
	}
}
