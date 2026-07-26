package action

import (
	"testing"
)

func Test_RecoveryCascade_SixLayers(t *testing.T) {
	rc := NewRecoveryCascade(2)

	// Layer 1: retry (attempts 1-2).
	layer, attempt := rc.Next()
	if layer != LayerRetry || attempt != 1 {
		t.Fatalf("expected retry attempt 1, got layer=%d attempt=%d", layer, attempt)
	}
	layer, attempt = rc.Next()
	if layer != LayerRetry || attempt != 2 {
		t.Fatalf("expected retry attempt 2, got layer=%d attempt=%d", layer, attempt)
	}

	// Layer 2: credential rotation.
	layer, attempt = rc.Next()
	if layer != LayerCredentialRotation || attempt != 3 {
		t.Fatalf("expected credential rotation, got layer=%d attempt=%d", layer, attempt)
	}

	// Layer 3: auth-profile rotation.
	layer, attempt = rc.Next()
	if layer != LayerAuthProfileRotation || attempt != 4 {
		t.Fatalf("expected auth-profile rotation, got layer=%d attempt=%d", layer, attempt)
	}

	// Layer 4: model fallback.
	layer, attempt = rc.Next()
	if layer != LayerModelFallback || attempt != 5 {
		t.Fatalf("expected model fallback, got layer=%d attempt=%d", layer, attempt)
	}

	// Layer 5: respawn.
	layer, attempt = rc.Next()
	if layer != LayerRespawn || attempt != 6 {
		t.Fatalf("expected respawn, got layer=%d attempt=%d", layer, attempt)
	}

	// Layer 6: honest failure.
	layer, attempt = rc.Next()
	if layer != LayerHonestFailure || attempt != 7 {
		t.Fatalf("expected honest failure, got layer=%d attempt=%d", layer, attempt)
	}
}

func Test_RecoveryCascade_Reset(t *testing.T) {
	rc := NewRecoveryCascade(1)
	rc.Next()
	rc.Next()
	rc.Reset()
	if rc.Attempt() != 0 {
		t.Fatalf("expected 0 after reset, got %d", rc.Attempt())
	}
}

func Test_RunLedger(t *testing.T) {
	ledger := NewRunLedger()
	ledger.Record(RunEntry{ID: "1", Outcome: OutcomeSuccess})
	ledger.Record(RunEntry{ID: "2", Outcome: OutcomeFailure})

	entries := ledger.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func Test_IdempotencyJoin(t *testing.T) {
	join := NewIdempotencyJoin()

	join.Register("op-1", RunEntry{ID: "1"})
	if !join.IsPending("op-1") {
		t.Fatal("expected op-1 to be pending")
	}
	if join.PendingCount() != 1 {
		t.Fatalf("expected 1 pending, got %d", join.PendingCount())
	}

	join.Resolve("op-1", RunEntry{ID: "1", Outcome: OutcomeSuccess})
	if join.IsPending("op-1") {
		t.Fatal("expected op-1 to be resolved")
	}
}

func Test_ErrIncomplete_Error(t *testing.T) {
	err := &ErrIncomplete{
		Phase:    "action",
		LastTool: "shell",
		Attempt:  3,
		Recovery: "retry",
	}
	if err.Error() == "" {
		t.Fatal("expected non-empty error")
	}
}

func Test_FailureClass_ConsequentialFailuresRemainDistinct(t *testing.T) {
	if FailureAuth == FailureRateLimit || FailureValidation == FailurePermanent {
		t.Fatal("recovery failure classes collapsed")
	}
}
