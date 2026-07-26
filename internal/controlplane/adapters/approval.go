package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/action"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
)

// BrokerAuthorizer adapts the control-plane approval broker to consequential
// operation handlers without exposing transport concepts to them.
type BrokerAuthorizer struct {
	Broker *controlplane.ApprovalBroker
	TTL    time.Duration
}

// AuthorizeTool connects the shared tool manager's RED boundary to the same
// durable approval broker used by control-plane operations.
func (authorizer BrokerAuthorizer) AuthorizeTool(
	ctx context.Context,
	invocation tools.Invocation,
) (context.Context, error) {
	principal := policy.PrincipalFromContext(ctx)
	if principal.Approved || principal.Sender != policy.SenderUser {
		return ctx, nil
	}
	if authorizer.Broker == nil {
		return ctx, fmt.Errorf("controlplane adapters: approval broker is required")
	}
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok {
		return ctx, nil
	}
	// Tool approvals resume an active durable turn. Standalone invocations have
	// no continuation to wake and retain the policy's immediate fail-closed
	// behavior.
	if scope.TurnID == nil {
		return ctx, nil
	}
	ttl := authorizer.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	_, err := authorizer.Broker.Request(ctx, controlplane.ApprovalInput{
		Scope: scope, Operation: invocation.Call.Name,
		Arguments:   invocation.Call.Arguments,
		Consequence: invocation.Description, TTL: ttl,
	})
	if err != nil {
		return ctx, err
	}
	principal.Approved = true
	return policy.WithPrincipal(ctx, principal), nil
}

// Authorize blocks only the background turn until an authenticated response.
func (authorizer BrokerAuthorizer) Authorize(
	ctx context.Context,
	manifest action.OperationManifest,
	arguments json.RawMessage,
) error {
	if authorizer.Broker == nil {
		return fmt.Errorf("controlplane adapters: approval broker is required")
	}
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok {
		return controlplane.ErrUnauthorized
	}
	ttl := authorizer.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	_, err := authorizer.Broker.Request(ctx, controlplane.ApprovalInput{
		Scope: scope, Operation: manifest.Name, Arguments: arguments,
		Consequence: manifest.Description, TTL: ttl,
	})
	return err
}
