package agent

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/belief/premise"
	"github.com/paxlabs-inc/ion-agent/internal/belief/selfmodel"
	"github.com/paxlabs-inc/ion-agent/internal/intent/taskgraph"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/decision"
	"github.com/paxlabs-inc/ion-agent/internal/reflection/cassandra"
	"github.com/paxlabs-inc/ion-agent/internal/security/circuit"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// PremiseLedger is the narrow interface the loop uses to manage premises.
type PremiseLedger interface {
	Add(statement string, source premise.Source, stepIndex int) (*premise.Premise, error)
	Attach(id uuid.UUID, subgoalIDs []string) error
	Cite(id uuid.UUID, citation protocol.Citation) error
	Refute(id uuid.UUID, citation *protocol.Citation) error
	Active() []*premise.Premise
	UnrevisedRefuted() []*premise.Premise
	HasRefuted() bool
	Render() string
	PlanChanged()
	ReviseAffected(subgoalIDs []string)
}

// PlanPremiseExtractor derives load-bearing premises from a committing plan.
type PlanPremiseExtractor interface {
	Extract(context.Context, premise.Plan) ([]premise.Premise, error)
}

// PredictionEngine is the narrow interface for prediction-carrying dispatch.
type PredictionEngine interface {
	BeginTurn()
	Observe(ctx context.Context, eventID uuid.UUID, toolName string, args json.RawMessage, expect string, result json.RawMessage, isError bool) (protocol.PredictionRecord, bool)
	ShouldForceRevision() bool
}

// PredictionRecordStore durably records expectation/outcome comparisons.
type PredictionRecordStore interface {
	RecordPrediction(context.Context, protocol.PredictionRecord) error
}

// TaskDAG is the narrow interface for the task graph.
type TaskDAG interface {
	AddSubgoal(text string) *taskgraph.Subgoal
	AddPremise(subgoalID string, statement string) (*taskgraph.Node, error)
	Current() *taskgraph.Subgoal
	ObserveAction(evidenceKey string, currentSubgoalID string) bool
	ShouldForceRevision() bool
	CompleteSubgoal(id string) error
	TodoProjection() []taskgraph.TodoItem
	Render() string
	ReplanSubtrees(subgoalIDs []string) error
	Reset()
}

// SelfModelManager is the narrow interface for self-model observations.
type SelfModelManager interface {
	AddCapability(event protocol.ToolEvent) error
	RecordFailure(failureMode string, toolName string)
	Snapshot() selfmodel.SelfModel
}

// CassandraController is the narrow interface for doubt control.
type CassandraController interface {
	Edit(msgID string, originalContent string, newContent string, trigger cassandra.Trigger, side cassandra.Side, reason string, actor string, approved bool) (*cassandra.Edit, error)
	Undo(editID *uuid.UUID) (*cassandra.Edit, error)
	NewTurn()
	IsProtected(memoryType string) bool
}

// CitationVerifier verifies citations against the MMR.
type CitationVerifier interface {
	VerifyCitation(ctx context.Context, citation protocol.Citation, event protocol.ToolEvent) (bool, error)
}

// ToolEventStore retrieves committed ToolEvents for citation verification.
type ToolEventStore interface {
	GetToolEvent(id uuid.UUID) (*protocol.ToolEvent, bool)
}

// ToolEventCommitter commits ToolEvents below the ciphertext hash boundary and
// returns the derived MMR receipt.
type ToolEventCommitter interface {
	CommitToolEvent(context.Context, protocol.ToolEvent) (*protocol.ToolEvent, error)
}

// CircuitBreaker blocks or converts a live turn into an honest partial before
// another provider or tool boundary is crossed.
type CircuitBreaker interface {
	Enforce(circuit.IncompleteContext) error
}

// MemoryActivation refreshes the complete authorized working set before each
// provider call. Implementations must scope retrieval to the supplied user and
// session rather than relying on process-global search.
type MemoryActivation interface {
	Activate(context.Context, string, string, string) (string, error)
}

// BehavioralModulator supplies decision-policy guidance derived from liveness
// state. It cannot alter the tool safety policy.
type BehavioralModulator interface {
	DecisionInstructions() string
}

// LivenessDecisionProvider supplies the typed policy that the loop enforces.
// The policy has no path to the tool safety classifier or approval broker.
type LivenessDecisionProvider interface {
	LivenessDecisionPolicy(
		context.Context,
		decision.Context,
	) (decision.LivenessDecisionPolicy, error)
}

// ContextSnapshot is the bounded immutable input to the production living
// context composition boundary.
type ContextSnapshot struct {
	UserID      string
	SessionID   string
	UserContent string
	Memory      string
	SelfModel   string
	Premises    string
	TaskGraph   string
}

// ContextComposer combines identity, relationship, temporal, emotional,
// memory, and epistemic state immediately before a provider call. It has no
// access to tool classification, approval, or execution.
type ContextComposer interface {
	Compose(context.Context, ContextSnapshot) (string, error)
}

// CheckpointSink durably persists resumable loop state and the cognition state
// mutated by the same step.
type CheckpointSink interface {
	SaveCheckpoint(context.Context, TurnCheckpoint) error
}
