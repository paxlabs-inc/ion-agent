package operatorapp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
)

// workAwareContextComposer adds the same durable contract visible to operator
// clients to the provider snapshot. It is guidance only; evidence-bound state
// transitions remain in work.Service.
type workAwareContextComposer struct {
	living agent.ContextComposer
	work   *workcontrol.Service
}

func (composer workAwareContextComposer) Compose(
	ctx context.Context,
	input agent.ContextSnapshot,
) (string, error) {
	base, err := composer.living.Compose(ctx, input)
	if err != nil {
		return "", err
	}
	if composer.work == nil {
		return base, nil
	}
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok || scope.SessionID == nil || scope.SessionID.String() != input.SessionID {
		return "", fmt.Errorf("operator work context: provider scope is not authenticated")
	}
	brief, err := composer.work.Brief(ctx, scope.ActorID, scope.SessionID)
	if err != nil {
		return "", err
	}
	projection, err := json.Marshal(brief)
	if err != nil {
		return "", fmt.Errorf("operator work context: encode brief: %w", err)
	}
	var builder strings.Builder
	builder.Grow(len(base) + len(projection) + 1024)
	builder.WriteString(base)
	builder.WriteString("\n## Disciplined work boundary\n")
	builder.WriteString("For substantial delegated work, establish or update an outcome contract before claiming progress. ")
	builder.WriteString("Use only relevant correctness, evidence, security, usability, and operability review lenses. ")
	builder.WriteString("Record deliverables, have Ion verify them, and never claim completion until every criterion has verified coverage. ")
	builder.WriteString("Autonomy settings are hard ceilings and never weaken policy or approvals.\n")
	builder.WriteString("Current derived Work Brief: ")
	builder.Write(projection)
	builder.WriteByte('\n')
	composed := builder.String()
	if len(composed) > 200<<10 {
		return "", fmt.Errorf("operator work context: composed snapshot exceeds bound")
	}
	return composed, nil
}
