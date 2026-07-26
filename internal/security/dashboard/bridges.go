package dashboard

import (
	"context"
	"fmt"

	"github.com/paxlabs-inc/ion-agent/internal/reflection/cassandra"
	"github.com/paxlabs-inc/ion-agent/internal/security/canary"
	"github.com/paxlabs-inc/ion-agent/internal/security/circuit"
	"github.com/paxlabs-inc/ion-agent/internal/security/memoryguard"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
)

// CircuitSink surfaces every circuit trigger to dashboard subscribers.
type CircuitSink struct {
	Dashboard *Dashboard
}

func (sink CircuitSink) CircuitBreakerTriggered(event circuit.Event) {
	if sink.Dashboard == nil {
		return
	}
	severity := SeverityWarning
	if event.Severity == circuit.SeverityCritical {
		severity = SeverityCritical
	}
	sink.Dashboard.Publish(
		EventCircuitBreaker,
		severity,
		"circuit_breaker",
		event.Reason,
		event,
	)
}

// CanarySink surfaces honeypot interactions to dashboard subscribers.
type CanarySink struct {
	Dashboard *Dashboard
}

func (sink CanarySink) CanaryAlerted(event canary.AlertEvent) {
	if sink.Dashboard == nil {
		return
	}
	severity := SeverityWarning
	if event.Operation == canary.OperationModify ||
		event.Operation == canary.OperationArchive ||
		event.Operation == canary.OperationIntegrity {
		severity = SeverityCritical
	}
	sink.Dashboard.Publish(
		EventCanaryAccess,
		severity,
		"memory_canary",
		fmt.Sprintf("honeypot %s attempt", event.Operation),
		event,
	)
}

// MemoryGuardSink surfaces protected-memory targeting attempts.
type MemoryGuardSink struct {
	Dashboard *Dashboard
}

func (sink MemoryGuardSink) ProtectedMemoryTargeted(event memoryguard.Event) {
	if sink.Dashboard == nil {
		return
	}
	sink.Dashboard.Publish(
		EventSafetyViolation,
		SeverityCritical,
		"memory_guard",
		fmt.Sprintf("protected memory type %s targeted", event.MemoryType),
		event,
	)
}

// CassandraAuditor durably records an edit before making it user-visible.
type CassandraAuditor struct {
	durable   cassandra.Auditor
	dashboard *Dashboard
}

func NewCassandraAuditor(
	durable cassandra.Auditor,
	dashboard *Dashboard,
) (*CassandraAuditor, error) {
	if durable == nil || dashboard == nil {
		return nil, fmt.Errorf("dashboard: Cassandra auditor and dashboard required")
	}
	return &CassandraAuditor{durable: durable, dashboard: dashboard}, nil
}

func (auditor *CassandraAuditor) RecordCassandraEvent(edit cassandra.Edit) error {
	if err := auditor.durable.RecordCassandraEvent(edit); err != nil {
		return err
	}
	redacted := cassandra.AuditEvent{
		EditID:        edit.ID,
		OriginalMsgID: edit.OriginalMsgID,
		OriginalHash:  edit.OriginalHash,
		Delta:         edit.Delta,
		Trigger:       edit.Trigger,
		Side:          edit.Side,
		Reason:        edit.Reason,
		Timestamp:     edit.Timestamp,
		Actor:         edit.Actor,
		Approved:      edit.Approved,
		ResultHash:    edit.ResultHash,
	}
	auditor.dashboard.Publish(
		EventCassandraEdit,
		SeverityWarning,
		"cassandra",
		"Cassandra edit recorded",
		redacted,
	)
	return nil
}

// PolicyAuditor durably records a policy event before making it user-visible.
type PolicyAuditor struct {
	durable   policy.Auditor
	dashboard *Dashboard
}

func NewPolicyAuditor(durable policy.Auditor, dashboard *Dashboard) (*PolicyAuditor, error) {
	if durable == nil || dashboard == nil {
		return nil, fmt.Errorf("dashboard: durable auditor and dashboard required")
	}
	return &PolicyAuditor{durable: durable, dashboard: dashboard}, nil
}

func (auditor *PolicyAuditor) RecordPolicyEvent(
	ctx context.Context,
	event policy.AuditEvent,
) error {
	if err := auditor.durable.RecordPolicyEvent(ctx, event); err != nil {
		return err
	}
	severity := SeverityInfo
	if event.Decision == policy.Deny {
		severity = SeverityWarning
	}
	auditor.dashboard.Publish(
		EventPolicyDecision,
		severity,
		"policy_pipeline",
		event.Reason,
		event,
	)
	return nil
}
