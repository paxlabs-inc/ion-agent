package privatecomputer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHostControllerPersistsReplayAndPersonalOwnership(t *testing.T) {
	now := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	stateRoot := filepath.Join(t.TempDir(), "state")
	workspaceRoot := filepath.Join(t.TempDir(), "workspaces")
	config := hostControllerTestConfig(
		stateRoot,
		workspaceRoot,
		ModePersonal,
		&now,
	)
	controller, err := NewHostController(config)
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope(ModePersonal)
	envelope := controllerEnvelope(
		now,
		scope,
		OperationProvision,
		1,
		"provision-personal",
	)
	envelope.Payload = lifecyclePayload(t, LifecyclePayload{
		Budget: budgetPointer(testBudget()),
	})
	result, err := controller.Execute(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Session.State != StateReady ||
		result.Session.Session.Revision != 2 ||
		result.Session.Workspace.Mode != ModePersonal ||
		!strings.Contains(
			result.Session.Workspace.Path,
			filepath.Join(
				scope.InstallationID.String(),
				scope.ActorID.String(),
			),
		) {
		t.Fatalf("personal provision = %+v", result)
	}
	marker := filepath.Join(result.Session.Workspace.Path, "retained.txt")
	if err := os.WriteFile(marker, []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}

	replayed, err := controller.Execute(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Replay != ReplayExact ||
		replayed.Receipt.RequestFingerprint != result.Receipt.RequestFingerprint {
		t.Fatalf("durable replay = %+v", replayed)
	}

	restarted, err := NewHostController(config)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err = restarted.Execute(context.Background(), envelope)
	if err != nil || replayed.Replay != ReplayExact {
		t.Fatalf("restart replay = %+v, %v", replayed, err)
	}
	if payload, readErr := os.ReadFile(marker); readErr != nil ||
		string(payload) != "retained" {
		t.Fatalf("personal marker = %q, %v", payload, readErr)
	}

	crossActor := controllerEnvelope(
		now,
		scope,
		OperationInspect,
		2,
		"inspect-cross-actor",
	)
	crossActor.Scope.ActorID = uuid.New()
	if _, err := restarted.Execute(
		context.Background(),
		crossActor,
	); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("cross-actor inspect = %v", err)
	}

	rebuild := controllerEnvelope(
		now,
		scope,
		OperationRebuild,
		2,
		"rebuild-personal",
	)
	rebuilt, err := restarted.Execute(context.Background(), rebuild)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Session.Workspace.Path != result.Session.Workspace.Path {
		t.Fatalf("personal rebuild changed workspace: %+v", rebuilt.Session.Workspace)
	}
	if payload, readErr := os.ReadFile(marker); readErr != nil ||
		string(payload) != "retained" {
		t.Fatalf("personal rebuild discarded marker = %q, %v", payload, readErr)
	}
}

func TestHostControllerCleanFreshnessArtifactGateAndCleanup(t *testing.T) {
	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	config := hostControllerTestConfig(
		filepath.Join(t.TempDir(), "state"),
		filepath.Join(t.TempDir(), "workspaces"),
		ModeClean,
		&now,
	)
	controller, err := NewHostController(config)
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope(ModeClean)
	provision := controllerEnvelope(
		now,
		scope,
		OperationProvision,
		1,
		"provision-clean",
	)
	provision.Payload = lifecyclePayload(t, LifecyclePayload{
		Budget: budgetPointer(testBudget()),
	})
	first, err := controller.Execute(context.Background(), provision)
	if err != nil {
		t.Fatal(err)
	}
	if first.Session.Workspace.FreshScopeDigest == "" ||
		first.Session.Workspace.DestructionDeadline == nil {
		t.Fatalf("clean scope is not fresh and bounded: %+v", first.Session.Workspace)
	}
	oldPath := first.Session.Workspace.Path
	if err := os.WriteFile(
		filepath.Join(oldPath, "clean-only.txt"),
		[]byte("ephemeral"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	rebuild := controllerEnvelope(
		now,
		scope,
		OperationRebuild,
		2,
		"rebuild-clean",
	)
	rebuilt, err := controller.Execute(context.Background(), rebuild)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Session.Workspace.Path == oldPath ||
		rebuilt.Session.Workspace.FreshScopeDigest ==
			first.Session.Workspace.FreshScopeDigest {
		t.Fatalf("clean rebuild reused scope: %+v", rebuilt.Session.Workspace)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old clean scope survived rebuild: %v", err)
	}

	produced := uuid.New()
	blocked := controllerEnvelope(
		now,
		scope,
		OperationDestroy,
		rebuilt.Session.Session.Revision,
		"destroy-clean-blocked",
	)
	blocked.Payload = lifecyclePayload(t, LifecyclePayload{
		ProducedArtifactIDs: []uuid.UUID{produced},
	})
	if _, err := controller.Execute(
		context.Background(),
		blocked,
	); !errors.Is(err, ErrArtifactRequired) {
		t.Fatalf("clean destroy without artifact export = %v", err)
	}
	if len(controller.Pending()) != 0 {
		t.Fatal("deterministically rejected destroy was journaled as uncertain")
	}

	destroy := controllerEnvelope(
		now,
		scope,
		OperationDestroy,
		rebuilt.Session.Session.Revision,
		"destroy-clean-empty",
	)
	destroyed, err := controller.Execute(context.Background(), destroy)
	if err != nil {
		t.Fatal(err)
	}
	if destroyed.Session.Session.State != StateDestroyed ||
		destroyed.CleanupEvidence == nil ||
		!destroyed.CleanupEvidence.WorkspaceRemoved ||
		destroyed.CleanupEvidence.Partial {
		t.Fatalf("clean cleanup = %+v", destroyed)
	}
	if _, err := os.Stat(rebuilt.Session.Workspace.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clean workspace survived destroy: %v", err)
	}
}

func TestHostControllerEnforcesStorageAndIdleBudgets(t *testing.T) {
	now := time.Date(2026, 7, 24, 16, 0, 0, 0, time.UTC)
	config := hostControllerTestConfig(
		filepath.Join(t.TempDir(), "state"),
		filepath.Join(t.TempDir(), "workspaces"),
		ModePersonal,
		&now,
	)
	controller, err := NewHostController(config)
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope(ModePersonal)
	budget := testBudget()
	budget.StorageBytes = 32
	budget.IdleSeconds = 1
	provision := controllerEnvelope(
		now,
		scope,
		OperationProvision,
		1,
		"provision-budget",
	)
	provision.Payload = lifecyclePayload(t, LifecyclePayload{
		Budget: &budget,
	})
	result, err := controller.Execute(context.Background(), provision)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(result.Session.Workspace.Path, "oversized"),
		[]byte(strings.Repeat("x", 64)),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if err := controller.EnforceBudgets(context.Background()); err != nil {
		t.Fatal(err)
	}
	enforced, exists := controller.Inspect(scope.ComputerSessionID)
	if !exists ||
		enforced.Session.State != StateUnavailable ||
		!strings.Contains(enforced.Session.UnavailableReason, "storage") {
		t.Fatalf("storage budget state = %+v", enforced)
	}
}

func TestDirectorySizeDoesNotFollowRuntimeSymlinks(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(
		outside,
		[]byte(strings.Repeat("x", 4096)),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "SingletonSocket")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	size, err := directorySize(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(outside)) {
		t.Fatalf("symlink usage = %d, want metadata size %d", size, len(outside))
	}
}

func hostControllerTestConfig(
	stateRoot string,
	workspaceRoot string,
	mode PersistenceMode,
	now *time.Time,
) HostControllerConfig {
	return HostControllerConfig{
		StateRoot:     stateRoot,
		WorkspaceRoot: workspaceRoot,
		Mode:          mode,
		HostID:        uuid.New(),
		HostVersion:   "ion-computer/0.1.0",
		ImageDigest:   "sha256:" + strings.Repeat("a", 64),
		Limits:        testBudget(),
		Clock:         func() time.Time { return *now },
	}
}

func controllerEnvelope(
	now time.Time,
	scope Scope,
	operation Operation,
	revision uint64,
	key string,
) Envelope {
	envelope := testEnvelope(now, scope)
	envelope.Operation = operation
	envelope.Resource = SessionResource(scope.ComputerSessionID)
	envelope.SessionRevision = revision
	envelope.IdempotencyKey = key
	envelope.ReplayNonce = key + "-replay-nonce-0000000000000000"
	envelope.Payload = nil
	return envelope
}

func lifecyclePayload(t *testing.T, payload LifecyclePayload) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func budgetPointer(value ResourceBudget) *ResourceBudget {
	return &value
}
