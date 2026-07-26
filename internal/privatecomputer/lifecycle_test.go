package privatecomputer

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLifecycleTransitionsBindScopeRevisionAndCleanArtifacts(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	session := testSession(now, ModePersonal, StateReady)
	envelope := testEnvelope(now, session.Scope)
	transition := Transition{
		Envelope: envelope,
		From:     StateReady,
		To:       StateActive,
	}
	updated, err := ApplyTransition(now.Add(time.Second), session, transition)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != StateActive || updated.Revision != session.Revision+1 {
		t.Fatalf("updated session = %+v", updated)
	}

	stale := transition
	stale.Envelope.SessionRevision--
	if _, err := ApplyTransition(now, session, stale); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale transition = %v", err)
	}

	crossActor := transition
	crossActor.Envelope.Scope.ActorID = uuid.New()
	if _, err := ApplyTransition(now, session, crossActor); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("cross-actor transition = %v", err)
	}

	illegal := transition
	illegal.To = StateDestroyed
	if _, err := ApplyTransition(now, session, illegal); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("illegal transition = %v", err)
	}

	clean := testSession(now, ModeClean, StateStopped)
	destroyEnvelope := testEnvelope(now, clean.Scope)
	destroyEnvelope.Operation = OperationDestroy
	produced := uuid.New()
	destroy := Transition{
		Envelope:            destroyEnvelope,
		From:                StateStopped,
		To:                  StateDestroyed,
		ProducedArtifactIDs: []uuid.UUID{produced},
	}
	if _, err := ApplyTransition(now, clean, destroy); !errors.Is(err, ErrArtifactRequired) {
		t.Fatalf("clean destroy without export = %v", err)
	}
	cleanup := uuid.New()
	destroy.ExportedArtifactIDs = []uuid.UUID{produced}
	destroy.CleanupEvidenceID = &cleanup
	destroyed, err := ApplyTransition(now, clean, destroy)
	if err != nil {
		t.Fatal(err)
	}
	if destroyed.State != StateDestroyed {
		t.Fatalf("destroyed session = %+v", destroyed)
	}
	if err := destroyed.Validate(); err != nil {
		t.Fatalf("durable destroyed session: %v", err)
	}
	if destroyed.CleanupEvidenceID == nil ||
		*destroyed.CleanupEvidenceID != cleanup ||
		len(destroyed.ExportedArtifactIDs) != 1 ||
		destroyed.ExportedArtifactIDs[0] != produced {
		t.Fatalf("cleanup evidence was not retained: %+v", destroyed)
	}
}

func TestLifecycleOperationMatrix(t *testing.T) {
	tests := []struct {
		operation Operation
		from      State
		to        State
	}{
		{OperationProvision, StateStopped, StateProvisioning},
		{OperationStart, StateReady, StateActive},
		{OperationStop, StateActive, StateStopped},
		{OperationSuspend, StateActive, StateSuspended},
		{OperationResume, StateSuspended, StateRecovering},
		{OperationRebuild, StateFailed, StateProvisioning},
		{OperationDestroy, StateStopped, StateDestroyed},
		{OperationDestroy, StateActive, StateDestroyed},
		{OperationInspect, StateReady, StateReady},
		{OperationReconcile, StateDisconnected, StateRecovering},
	}
	for _, test := range tests {
		if !operationAllows(test.operation, test.from, test.to) {
			t.Errorf("%s rejected %s -> %s", test.operation, test.from, test.to)
		}
	}
	if operationAllows(OperationStart, StateFailed, StateActive) {
		t.Fatal("failed session started without rebuild")
	}
	if operationAllows(OperationDestroy, StateDestroyed, StateDestroyed) {
		t.Fatal("destroyed session accepted another operation")
	}
	if operationAllows(Operation("computer.unknown"), StateReady, StateActive) {
		t.Fatal("unknown operation was accepted")
	}
}

func TestRestartReconciliationIsExplicitAndFailClosed(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	session := testSession(now, ModeClean, StateActive)
	observation := testObservation(now, session)
	observation.Orphaned = true
	reconciled, err := Reconcile(
		now.Add(time.Second),
		[]string{ProtocolVersion},
		session,
		observation,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.State != StateRecovering ||
		len(reconciled.DegradedReasons) != 1 {
		t.Fatalf("orphan reconciliation = %+v", reconciled)
	}

	incompatible := testObservation(now, session)
	incompatible.HostVersion = "ion-computer/9.0.0"
	reconciled, err = Reconcile(
		now.Add(time.Second),
		[]string{ProtocolVersion},
		session,
		incompatible,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.State != StateUnavailable ||
		reconciled.UnavailableReason == "" {
		t.Fatalf("incompatible reconciliation = %+v", reconciled)
	}

	stale := testObservation(now, session)
	stale.SessionRevision--
	if _, err := Reconcile(
		now,
		[]string{ProtocolVersion},
		session,
		stale,
	); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale observation = %v", err)
	}

	crossMode := testObservation(now, session)
	crossMode.Scope.Mode = ModePersonal
	if _, err := Reconcile(
		now,
		[]string{ProtocolVersion},
		session,
		crossMode,
	); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("cross-mode observation = %v", err)
	}
}

func testSession(now time.Time, mode PersistenceMode, state State) Session {
	scope := testScope(mode)
	reason := ""
	if state == StateUnavailable {
		reason = "host unavailable"
	}
	return Session{
		ID:                scope.ComputerSessionID,
		Scope:             scope,
		State:             state,
		Revision:          3,
		AuthorityRevision: 4,
		HostID:            uuid.New(),
		HostVersion:       "ion-computer/0.1.0",
		ImageDigest:       "sha256:" + strings.Repeat("a", 64),
		Budget:            testBudget(),
		UnavailableReason: reason,
		CreatedAt:         now.Add(-time.Hour),
		UpdatedAt:         now,
	}
}

func testObservation(now time.Time, session Session) Observation {
	return Observation{
		ProtocolVersion:   ProtocolVersion,
		Scope:             session.Scope,
		HostID:            session.HostID,
		HostVersion:       session.HostVersion,
		ImageDigest:       session.ImageDigest,
		State:             session.State,
		SessionRevision:   session.Revision,
		AuthorityRevision: session.AuthorityRevision,
		ObservedAt:        now,
	}
}
