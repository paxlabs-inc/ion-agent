package privatecomputer

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID                  uuid.UUID      `json:"id"`
	Scope               Scope          `json:"scope"`
	State               State          `json:"state"`
	Revision            uint64         `json:"revision"`
	AuthorityRevision   uint64         `json:"authority_revision"`
	HostID              uuid.UUID      `json:"host_id"`
	HostVersion         string         `json:"host_version"`
	ImageDigest         string         `json:"image_digest"`
	Budget              ResourceBudget `json:"budget"`
	UnavailableReason   string         `json:"unavailable_reason,omitempty"`
	DegradedReasons     []string       `json:"degraded_reasons,omitempty"`
	ExportedArtifactIDs []uuid.UUID    `json:"exported_artifact_ids,omitempty"`
	CleanupEvidenceID   *uuid.UUID     `json:"cleanup_evidence_id,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

func (session Session) Validate() error {
	if session.ID == uuid.Nil || session.ID != session.Scope.ComputerSessionID ||
		session.Scope.Validate() != nil || !session.State.valid() ||
		session.Revision == 0 || session.AuthorityRevision == 0 ||
		session.HostID == uuid.Nil || strings.TrimSpace(session.HostVersion) == "" ||
		!validImageDigest(session.ImageDigest) ||
		session.Budget.Validate() != nil || session.CreatedAt.IsZero() ||
		session.UpdatedAt.IsZero() || session.UpdatedAt.Before(session.CreatedAt) ||
		len(session.UnavailableReason) > MaximumReasonLength ||
		len(session.DegradedReasons) > 32 ||
		len(session.ExportedArtifactIDs) > MaximumArtifactLinks ||
		!validUUIDs(session.ExportedArtifactIDs) {
		return ErrInvalidContract
	}
	if session.CleanupEvidenceID != nil && *session.CleanupEvidenceID == uuid.Nil {
		return ErrInvalidContract
	}
	if session.Scope.Mode == ModeClean && session.State == StateDestroyed &&
		session.CleanupEvidenceID == nil {
		return ErrInvalidContract
	}
	if session.State == StateUnavailable &&
		strings.TrimSpace(session.UnavailableReason) == "" {
		return ErrInvalidContract
	}
	if session.State != StateUnavailable &&
		strings.TrimSpace(session.UnavailableReason) != "" {
		return ErrInvalidContract
	}
	for _, reason := range session.DegradedReasons {
		if strings.TrimSpace(reason) == "" || len(reason) > MaximumReasonLength {
			return ErrInvalidContract
		}
	}
	return nil
}

type Transition struct {
	Envelope            Envelope    `json:"envelope"`
	From                State       `json:"from"`
	To                  State       `json:"to"`
	ProducedArtifactIDs []uuid.UUID `json:"produced_artifact_ids,omitempty"`
	ExportedArtifactIDs []uuid.UUID `json:"exported_artifact_ids,omitempty"`
	CleanupEvidenceID   *uuid.UUID  `json:"cleanup_evidence_id,omitempty"`
}

func ApplyTransition(
	now time.Time,
	session Session,
	transition Transition,
) (Session, error) {
	if err := session.Validate(); err != nil {
		return Session{}, err
	}
	if err := transition.Envelope.Validate(now); err != nil {
		return Session{}, err
	}
	if !session.Scope.SameAuthority(transition.Envelope.Scope) {
		return Session{}, ErrScopeMismatch
	}
	if transition.Envelope.AuthorityRevision != session.AuthorityRevision ||
		transition.Envelope.SessionRevision != session.Revision {
		return Session{}, ErrStaleRevision
	}
	if transition.From != session.State ||
		!transition.To.valid() ||
		!operationAllows(
			transition.Envelope.Operation,
			transition.From,
			transition.To,
		) {
		return Session{}, ErrInvalidTransition
	}
	if len(transition.ProducedArtifactIDs) > MaximumArtifactLinks ||
		len(transition.ExportedArtifactIDs) > MaximumArtifactLinks ||
		!validUUIDs(transition.ProducedArtifactIDs) ||
		!validUUIDs(transition.ExportedArtifactIDs) {
		return Session{}, ErrInvalidContract
	}
	if transition.Envelope.Operation == OperationDestroy &&
		session.Scope.Mode == ModeClean {
		if transition.CleanupEvidenceID == nil ||
			*transition.CleanupEvidenceID == uuid.Nil ||
			!containsAll(
				transition.ExportedArtifactIDs,
				transition.ProducedArtifactIDs,
			) {
			return Session{}, ErrArtifactRequired
		}
	}
	if transition.CleanupEvidenceID != nil &&
		*transition.CleanupEvidenceID == uuid.Nil {
		return Session{}, ErrInvalidContract
	}
	session.State = transition.To
	session.Revision++
	session.UpdatedAt = now.UTC()
	session.ExportedArtifactIDs = append(
		[]uuid.UUID(nil),
		transition.ExportedArtifactIDs...,
	)
	session.CleanupEvidenceID = copyUUID(transition.CleanupEvidenceID)
	session.UnavailableReason = ""
	if transition.To == StateUnavailable {
		session.UnavailableReason = "host reported unavailable"
	}
	return session, nil
}

type Observation struct {
	ProtocolVersion   string    `json:"protocol_version"`
	Scope             Scope     `json:"scope"`
	HostID            uuid.UUID `json:"host_id"`
	HostVersion       string    `json:"host_version"`
	ImageDigest       string    `json:"image_digest"`
	State             State     `json:"state"`
	SessionRevision   uint64    `json:"session_revision"`
	AuthorityRevision uint64    `json:"authority_revision"`
	ObservedAt        time.Time `json:"observed_at"`
	Orphaned          bool      `json:"orphaned"`
	Reason            string    `json:"reason,omitempty"`
}

func Reconcile(
	now time.Time,
	supportedProtocolVersions []string,
	session Session,
	observation Observation,
) (Session, error) {
	if err := session.Validate(); err != nil {
		return Session{}, err
	}
	if !session.Scope.SameAuthority(observation.Scope) {
		return Session{}, ErrScopeMismatch
	}
	if observation.ProtocolVersion == "" ||
		observation.HostID != session.HostID ||
		observation.HostVersion == "" ||
		!validImageDigest(observation.ImageDigest) ||
		!observation.State.valid() ||
		observation.SessionRevision == 0 ||
		observation.AuthorityRevision == 0 ||
		observation.ObservedAt.IsZero() ||
		observation.ObservedAt.After(now.Add(time.Minute)) ||
		len(observation.Reason) > MaximumReasonLength {
		return Session{}, ErrInvalidContract
	}
	if !slices.Contains(supportedProtocolVersions, observation.ProtocolVersion) ||
		observation.HostVersion != session.HostVersion ||
		observation.ImageDigest != session.ImageDigest {
		session.State = StateUnavailable
		session.Revision++
		session.UpdatedAt = now.UTC()
		session.UnavailableReason = "private computer host is incompatible"
		return session, nil
	}
	if observation.AuthorityRevision != session.AuthorityRevision ||
		observation.SessionRevision < session.Revision {
		return Session{}, ErrStaleRevision
	}
	if observation.SessionRevision == session.Revision &&
		observation.State != session.State && !observation.Orphaned {
		return Session{}, ErrInvalidTransition
	}
	if observation.Orphaned {
		session.State = StateRecovering
		session.Revision = max(session.Revision, observation.SessionRevision) + 1
		session.UpdatedAt = now.UTC()
		session.UnavailableReason = ""
		session.DegradedReasons = appendBoundedReason(
			session.DegradedReasons,
			"host reported an orphaned private computer session",
		)
		return session, nil
	}
	if observation.State != session.State &&
		!allowedStateTransition(session.State, observation.State) {
		return Session{}, ErrInvalidTransition
	}
	session.State = observation.State
	session.Revision = max(session.Revision, observation.SessionRevision)
	session.UpdatedAt = now.UTC()
	session.UnavailableReason = ""
	if observation.State == StateUnavailable {
		if strings.TrimSpace(observation.Reason) == "" {
			return Session{}, fmt.Errorf(
				"%w: unavailable observation requires a reason",
				ErrInvalidContract,
			)
		}
		session.UnavailableReason = observation.Reason
	}
	return session, nil
}

func operationAllows(operation Operation, from, to State) bool {
	if !allowedStateTransition(from, to) && from != to {
		return false
	}
	switch operation {
	case OperationProvision:
		return to == StateProvisioning &&
			(from == StateStopped || from == StateUnavailable ||
				from == StateFailed)
	case OperationStart:
		return to == StateActive &&
			(from == StateReady || from == StateStopped)
	case OperationStop:
		return to == StateStopped && from != StateDestroyed
	case OperationSuspend:
		return to == StateSuspended &&
			(from == StateReady || from == StateActive ||
				from == StateNeedsHelp)
	case OperationResume:
		return to == StateRecovering && from == StateSuspended
	case OperationRebuild:
		return to == StateProvisioning &&
			(from == StateStopped || from == StateUnavailable ||
				from == StateFailed || from == StateReady)
	case OperationDestroy:
		return to == StateDestroyed && from != StateDestroyed
	case OperationInspect:
		return to == from
	case OperationReconcile:
		return from == to || allowedStateTransition(from, to)
	default:
		return false
	}
}

func allowedStateTransition(from, to State) bool {
	if !from.valid() || !to.valid() || from == StateDestroyed {
		return false
	}
	allowed := map[State][]State{
		StateStopped: {
			StateProvisioning, StateReady, StateActive, StateUnavailable,
			StateDestroyed,
		},
		StateUnavailable: {
			StateProvisioning, StateRecovering, StateStopped, StateDestroyed,
		},
		StateProvisioning: {
			StateReady, StateUnavailable, StateFailed, StateStopped,
			StateDestroyed,
		},
		StateReady: {
			StateActive, StateStopped, StateSuspended, StateProvisioning, StateUnavailable,
			StateRecovering, StateFailed, StateDestroyed,
		},
		StateActive: {
			StateNeedsHelp, StateDisconnected, StateRecovering, StateStopped,
			StateSuspended, StateUnavailable, StateFailed, StateDestroyed,
		},
		StateNeedsHelp: {
			StateActive, StateStopped, StateSuspended, StateDisconnected,
			StateRecovering, StateUnavailable, StateFailed, StateDestroyed,
		},
		StateDisconnected: {
			StateRecovering, StateStopped, StateUnavailable, StateFailed,
			StateDestroyed,
		},
		StateRecovering: {
			StateReady, StateActive, StateStopped, StateUnavailable, StateFailed,
			StateDestroyed,
		},
		StateSuspended: {
			StateRecovering, StateStopped, StateUnavailable, StateDestroyed,
		},
		StateFailed: {
			StateProvisioning, StateRecovering, StateStopped, StateUnavailable,
			StateDestroyed,
		},
	}
	return slices.Contains(allowed[from], to)
}

func containsAll(haystack, needles []uuid.UUID) bool {
	present := make(map[uuid.UUID]struct{}, len(haystack))
	for _, id := range haystack {
		present[id] = struct{}{}
	}
	for _, id := range needles {
		if _, exists := present[id]; !exists {
			return false
		}
	}
	return true
}

func validUUIDs(ids []uuid.UUID) bool {
	for _, id := range ids {
		if id == uuid.Nil {
			return false
		}
	}
	return true
}

func appendBoundedReason(existing []string, reason string) []string {
	for _, current := range existing {
		if current == reason {
			return existing
		}
	}
	if len(existing) >= 32 {
		return existing
	}
	return append(existing, reason)
}

func copyUUID(id *uuid.UUID) *uuid.UUID {
	if id == nil {
		return nil
	}
	copied := *id
	return &copied
}
