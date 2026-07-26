package operatorapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	nativebrowser "github.com/paxlabs-inc/ion-agent/internal/browser"
	"github.com/paxlabs-inc/ion-agent/internal/controllease"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
)

type computerResourceInput struct {
	ResourceKind   controllease.ResourceKind `json:"resource_kind"`
	ResourceID     string                    `json:"resource_id,omitempty"`
	TargetRevision uint64                    `json:"target_revision,omitempty"`
}

type computerOwnerInput struct {
	TurnID      *uuid.UUID `json:"turn_id,omitempty"`
	TaskID      *uuid.UUID `json:"task_id,omitempty"`
	AgentID     string     `json:"agent_id"`
	ToolEventID *uuid.UUID `json:"tool_event_id,omitempty"`
	Action      string     `json:"action"`
	Revision    uint64     `json:"revision"`
}

type computerAcquireInput struct {
	computerResourceInput
	Owner                 computerOwnerInput `json:"owner"`
	ExpectedLeaseRevision uint64             `json:"expected_lease_revision"`
	TTLSeconds            int                `json:"ttl_seconds,omitempty"`
}

type computerLeaseInput struct {
	computerResourceInput
	LeaseID               uuid.UUID `json:"lease_id"`
	ExpectedLeaseRevision uint64    `json:"expected_lease_revision"`
	TTLSeconds            int       `json:"ttl_seconds,omitempty"`
}

func registerComputerControlHandlers(
	dispatcher *controlplane.Dispatcher,
	capabilities *productionCapabilities,
	journal *controlplane.Journal,
) error {
	if dispatcher == nil || capabilities == nil || capabilities.control == nil ||
		capabilities.browser == nil || capabilities.projects == nil || journal == nil {
		return fmt.Errorf("operator computer control: production dependencies are required")
	}
	register := func(
		operation controlplane.Operation,
		description string,
		handler controlplane.HandlerFunc,
	) error {
		return dispatcher.Register(operation, description, handler)
	}
	if err := register(
		controlplane.OperationComputerControlGet,
		"Inspect the durable executor or operator authority for one browser or terminal resource.",
		func(ctx context.Context, request controlplane.Request, _ controlplane.EventEmitter) (json.RawMessage, error) {
			var input computerResourceInput
			if err := decodeStrictJSON(request.Payload, &input); err != nil {
				return nil, err
			}
			target, err := computerControlTarget(request.Scope, input)
			if err != nil {
				return nil, err
			}
			if input.ResourceKind == controllease.ResourceTerminal {
				target, err = authoritativeTerminalTarget(
					ctx, capabilities, request.Scope, input,
				)
				if err != nil {
					return nil, err
				}
			}
			lease, err := capabilities.control.Status(ctx, target)
			if err != nil {
				return nil, computerControlPublicError(err)
			}
			if lease.Owner.AgentID == "" {
				if owner, ownerErr := resolveComputerOwner(
					ctx, capabilities, journal, request.Scope, input,
				); ownerErr == nil {
					lease.Owner = owner
				}
			}
			return json.Marshal(lease)
		},
	); err != nil {
		return err
	}
	if err := register(
		controlplane.OperationComputerControlAcquire,
		"Acquire revision-bound operator authority after the owning executor reaches an action boundary.",
		func(ctx context.Context, request controlplane.Request, emitter controlplane.EventEmitter) (json.RawMessage, error) {
			var input computerAcquireInput
			if err := decodeStrictJSON(request.Payload, &input); err != nil {
				return nil, err
			}
			target, err := computerControlTarget(request.Scope, input.computerResourceInput)
			if err != nil {
				return nil, err
			}
			if input.ResourceKind == controllease.ResourceTerminal {
				target, err = authoritativeTerminalTarget(
					ctx, capabilities, request.Scope, input.computerResourceInput,
				)
				if err != nil {
					return nil, err
				}
				terminalID, _ := uuid.Parse(input.ResourceID)
				if !capabilities.projects.TerminalControlRunning(
					request.Scope.ActorID, terminalID,
				) {
					return nil, controlplane.PublicError{
						Code:    controlplane.ErrorConflict,
						Message: "the terminal executor is no longer running",
					}
				}
			}
			owner, err := resolveComputerOwner(
				ctx, capabilities, journal, request.Scope, input.computerResourceInput,
			)
			if err != nil {
				return nil, err
			}
			if !sameComputerOwner(owner, input.Owner) {
				return nil, controlplane.PublicError{
					Code:    controlplane.ErrorConflict,
					Message: "the executor target changed before control was acquired",
				}
			}
			lease, err := capabilities.control.Acquire(
				ctx, target, owner, input.ExpectedLeaseRevision,
				time.Duration(input.TTLSeconds)*time.Second,
			)
			if err != nil {
				return nil, computerControlPublicError(err)
			}
			if err := emitComputerControl(
				ctx, emitter, controlplane.EventComputerControlAcquired, lease,
			); err != nil {
				return nil, err
			}
			return json.Marshal(lease)
		},
	); err != nil {
		return err
	}
	if err := register(
		controlplane.OperationComputerControlRenew,
		"Renew the exact active operator lease without changing its executor target.",
		func(ctx context.Context, request controlplane.Request, emitter controlplane.EventEmitter) (json.RawMessage, error) {
			var input computerLeaseInput
			if err := decodeStrictJSON(request.Payload, &input); err != nil {
				return nil, err
			}
			target, err := computerControlTarget(request.Scope, input.computerResourceInput)
			if err != nil {
				return nil, err
			}
			lease, err := capabilities.control.Renew(
				ctx, target, input.LeaseID, input.ExpectedLeaseRevision,
				time.Duration(input.TTLSeconds)*time.Second,
			)
			if err != nil {
				return nil, computerControlPublicError(err)
			}
			if err := emitComputerControl(
				ctx, emitter, controlplane.EventComputerControlRenewed, lease,
			); err != nil {
				return nil, err
			}
			return json.Marshal(lease)
		},
	); err != nil {
		return err
	}
	if err := register(
		controlplane.OperationComputerControlRelease,
		"Release the exact active operator lease and reconcile authority to the executor.",
		func(ctx context.Context, request controlplane.Request, emitter controlplane.EventEmitter) (json.RawMessage, error) {
			var input computerLeaseInput
			if err := decodeStrictJSON(request.Payload, &input); err != nil {
				return nil, err
			}
			target, err := computerControlTarget(request.Scope, input.computerResourceInput)
			if err != nil {
				return nil, err
			}
			lease, err := capabilities.control.Release(
				ctx, target, input.LeaseID, input.ExpectedLeaseRevision,
			)
			if err != nil {
				return nil, computerControlPublicError(err)
			}
			if err := emitComputerControl(
				ctx, emitter, controlplane.EventComputerControlReleased, lease,
			); err != nil {
				return nil, err
			}
			return json.Marshal(lease)
		},
	); err != nil {
		return err
	}
	if err := register(
		controlplane.OperationComputerBrowserObserve,
		"Observe the actor and session browser only while holding its exact operator lease.",
		func(ctx context.Context, request controlplane.Request, _ controlplane.EventEmitter) (json.RawMessage, error) {
			var input computerLeaseInput
			if err := decodeStrictJSON(request.Payload, &input); err != nil {
				return nil, err
			}
			runCtx, err := computerBrowserContext(ctx, request.Scope, input)
			if err != nil {
				return nil, err
			}
			snapshot, err := capabilities.browser.ObserveWithLease(
				runCtx, input.LeaseID, input.ExpectedLeaseRevision,
			)
			if err != nil {
				return nil, computerControlPublicError(err)
			}
			return json.Marshal(snapshot)
		},
	); err != nil {
		return err
	}
	if err := registerComputerBrowserCommands(dispatcher, capabilities); err != nil {
		return err
	}
	return nil
}

func authoritativeTerminalTarget(
	ctx context.Context,
	capabilities *productionCapabilities,
	scope controlplane.Scope,
	input computerResourceInput,
) (controllease.Target, error) {
	terminalID, err := uuid.Parse(strings.TrimSpace(input.ResourceID))
	if err != nil || terminalID == uuid.Nil {
		return controllease.Target{}, controlplane.PublicError{
			Code:    controlplane.ErrorInvalid,
			Message: "terminal control requires a valid resource_id",
		}
	}
	target, _, err := capabilities.projects.TerminalControlBinding(
		ctx, scope.ActorID, terminalID,
	)
	if err != nil {
		return controllease.Target{}, computerControlPublicError(err)
	}
	if !sameControlUUID(target.SessionID, scope.SessionID) {
		return controllease.Target{}, controlplane.ErrUnauthorized
	}
	return target, nil
}

func registerComputerBrowserCommands(
	dispatcher *controlplane.Dispatcher,
	capabilities *productionCapabilities,
) error {
	type browserActionInput struct {
		computerLeaseInput
		URL    string `json:"url,omitempty"`
		Action string `json:"action,omitempty"`
		Ref    string `json:"ref,omitempty"`
		Value  string `json:"value,omitempty"`
	}
	for _, registration := range []struct {
		operation   controlplane.Operation
		description string
		run         func(context.Context, browserActionInput) (nativebrowser.Snapshot, error)
	}{
		{
			operation:   controlplane.OperationComputerBrowserNavigate,
			description: "Navigate the actor and session browser while its executor is paused by the exact operator lease.",
			run: func(ctx context.Context, input browserActionInput) (nativebrowser.Snapshot, error) {
				return capabilities.browser.NavigateWithLease(
					ctx, input.LeaseID, input.ExpectedLeaseRevision, input.URL,
				)
			},
		},
		{
			operation:   controlplane.OperationComputerBrowserInteract,
			description: "Perform one reversible browser interaction under the exact operator lease.",
			run: func(ctx context.Context, input browserActionInput) (nativebrowser.Snapshot, error) {
				return capabilities.browser.InteractWithLease(
					ctx, input.LeaseID, input.ExpectedLeaseRevision,
					input.Action, input.Ref, input.Value,
				)
			},
		},
		{
			operation:   controlplane.OperationComputerBrowserSubmit,
			description: "Activate one consequential browser control as the lease-holding human operator.",
			run: func(ctx context.Context, input browserActionInput) (nativebrowser.Snapshot, error) {
				return capabilities.browser.SubmitWithLease(
					ctx, input.LeaseID, input.ExpectedLeaseRevision, input.Ref,
				)
			},
		},
	} {
		current := registration
		if err := dispatcher.Register(
			current.operation,
			current.description,
			controlplane.HandlerFunc(func(
				ctx context.Context,
				request controlplane.Request,
				_ controlplane.EventEmitter,
			) (json.RawMessage, error) {
				var input browserActionInput
				if err := decodeStrictJSON(request.Payload, &input); err != nil {
					return nil, err
				}
				runCtx, err := computerBrowserContext(
					ctx, request.Scope, input.computerLeaseInput,
				)
				if err != nil {
					return nil, err
				}
				snapshot, err := current.run(runCtx, input)
				if err != nil {
					return nil, computerControlPublicError(err)
				}
				return json.Marshal(snapshot)
			}),
		); err != nil {
			return err
		}
	}
	return nil
}

func computerControlTarget(
	scope controlplane.Scope,
	input computerResourceInput,
) (controllease.Target, error) {
	switch input.ResourceKind {
	case controllease.ResourceBrowser:
		if scope.SessionID == nil {
			return controllease.Target{}, controlplane.PublicError{
				Code:    controlplane.ErrorInvalid,
				Message: "browser control requires an explicit session scope",
			}
		}
		target := nativebrowser.ControlTarget(scope.ActorID, scope.SessionID)
		if resourceID := strings.TrimSpace(input.ResourceID); resourceID != "" &&
			resourceID != target.ResourceID {
			return controllease.Target{}, controlplane.PublicError{
				Code:    controlplane.ErrorConflict,
				Message: "browser resource does not match the authenticated session",
			}
		}
		return target, nil
	case controllease.ResourceDesktop:
		if scope.SessionID == nil {
			return controllease.Target{}, controlplane.PublicError{
				Code:    controlplane.ErrorInvalid,
				Message: "desktop control requires an explicit session scope",
			}
		}
		resourceID := strings.TrimSpace(input.ResourceID)
		if resourceID != "" && resourceID != scope.SessionID.String() {
			return controllease.Target{}, controlplane.PublicError{
				Code:    controlplane.ErrorConflict,
				Message: "desktop resource does not match the authenticated session",
			}
		}
		return controllease.Target{
			ActorID:    scope.ActorID,
			SessionID:  copyControlUUID(scope.SessionID),
			Kind:       controllease.ResourceDesktop,
			ResourceID: scope.SessionID.String(),
		}, nil
	case controllease.ResourceTerminal:
		resourceID, err := uuid.Parse(strings.TrimSpace(input.ResourceID))
		if err != nil || resourceID == uuid.Nil {
			return controllease.Target{}, controlplane.PublicError{
				Code:    controlplane.ErrorInvalid,
				Message: "terminal control requires a valid resource_id",
			}
		}
		return controllease.Target{
			ActorID: scope.ActorID, SessionID: copyControlUUID(scope.SessionID),
			Kind: controllease.ResourceTerminal, ResourceID: resourceID.String(),
		}, nil
	default:
		return controllease.Target{}, controlplane.PublicError{
			Code:    controlplane.ErrorInvalid,
			Message: "resource_kind must be browser, desktop, or terminal",
		}
	}
}

func resolveComputerOwner(
	ctx context.Context,
	capabilities *productionCapabilities,
	journal *controlplane.Journal,
	scope controlplane.Scope,
	input computerResourceInput,
) (controllease.Owner, error) {
	switch input.ResourceKind {
	case controllease.ResourceDesktop:
		if scope.SessionID == nil {
			return controllease.Owner{}, controlplane.PublicError{
				Code:    controlplane.ErrorInvalid,
				Message: "desktop control requires a session",
			}
		}
		revision := input.TargetRevision
		if revision == 0 {
			revision = 1
		}
		taskID := *scope.SessionID
		return controllease.Owner{
			TaskID:   &taskID,
			AgentID:  "ion",
			Action:   "private_desktop",
			Revision: revision,
		}, nil
	case controllease.ResourceTerminal:
		terminalID, _ := uuid.Parse(strings.TrimSpace(input.ResourceID))
		_, owner, err := capabilities.projects.TerminalControlBinding(
			ctx, scope.ActorID, terminalID,
		)
		if err != nil {
			return controllease.Owner{}, computerControlPublicError(err)
		}
		if input.TargetRevision != 0 && owner.Revision != input.TargetRevision {
			return controllease.Owner{}, controlplane.PublicError{
				Code:    controlplane.ErrorConflict,
				Message: "the terminal executor target is stale",
			}
		}
		return owner, nil
	case controllease.ResourceBrowser:
		return browserOwnerFromJournal(
			ctx, journal, scope, input.TargetRevision,
		)
	default:
		return controllease.Owner{}, controlplane.PublicError{
			Code: controlplane.ErrorInvalid, Message: "unsupported control resource",
		}
	}
}

func browserOwnerFromJournal(
	ctx context.Context,
	journal *controlplane.Journal,
	scope controlplane.Scope,
	targetRevision uint64,
) (controllease.Owner, error) {
	var latest *controlplane.Event
	var latestBrowserRevision uint64
	var after uint64
	for {
		replay, err := journal.ReplayActor(ctx, scope.ActorID, after, 2_000)
		if err != nil {
			return controllease.Owner{}, err
		}
		for index := range replay.Events {
			event := replay.Events[index]
			if event.Correlation.SessionID == nil || scope.SessionID == nil ||
				*event.Correlation.SessionID != *scope.SessionID ||
				!event.Type.Valid() {
				continue
			}
			var payload controlplane.ComputerEventPayload
			if json.Unmarshal(event.Payload, &payload) != nil ||
				!strings.HasPrefix(payload.Tool, "browser_") {
				continue
			}
			if event.Sequence > latestBrowserRevision {
				latestBrowserRevision = event.Sequence
			}
			if targetRevision != 0 && event.Sequence == targetRevision {
				copied := event
				latest = &copied
			}
			if targetRevision == 0 && (latest == nil || event.Sequence > latest.Sequence) {
				copied := event
				latest = &copied
			}
		}
		if replay.Latest >= replay.Head || len(replay.Events) == 0 {
			break
		}
		after = replay.Latest
	}
	if latest == nil || (targetRevision != 0 && latest.Sequence != targetRevision) {
		return controllease.Owner{}, controlplane.PublicError{
			Code:    controlplane.ErrorNotFound,
			Message: "the targeted browser action is not retained",
		}
	}
	if targetRevision != 0 && targetRevision != latestBrowserRevision {
		return controllease.Owner{}, controlplane.PublicError{
			Code:    controlplane.ErrorConflict,
			Message: "the targeted browser action is stale",
		}
	}
	var payload controlplane.ComputerEventPayload
	if err := json.Unmarshal(latest.Payload, &payload); err != nil {
		return controllease.Owner{}, err
	}
	taskID := copyControlUUID(payload.Scope.TaskID)
	if taskID == nil {
		taskID = copyControlUUID(payload.Scope.OutcomeID)
	}
	if taskID == nil {
		taskID = copyControlUUID(payload.Scope.TurnID)
	}
	if taskID == nil {
		return controllease.Owner{}, controlplane.PublicError{
			Code:    controlplane.ErrorConflict,
			Message: "the browser action lacks an authoritative task target",
		}
	}
	toolEventID := payload.ToolEventID
	return controllease.Owner{
		TurnID: copyControlUUID(payload.Scope.TurnID),
		TaskID: taskID, AgentID: payload.Scope.AgentID,
		ToolEventID: &toolEventID, Action: payload.Operation,
		Revision: latest.Sequence,
	}, nil
}

func computerBrowserContext(
	ctx context.Context,
	scope controlplane.Scope,
	input computerLeaseInput,
) (context.Context, error) {
	if input.ResourceKind != controllease.ResourceBrowser ||
		scope.SessionID == nil || input.LeaseID == uuid.Nil ||
		input.ExpectedLeaseRevision == 0 {
		return nil, controlplane.PublicError{
			Code:    controlplane.ErrorInvalid,
			Message: "an exact browser lease and session are required",
		}
	}
	return controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
		ActorID: scope.ActorID, SessionID: scope.SessionID, AgentID: "operator",
	}), nil
}

func emitComputerControl(
	ctx context.Context,
	emitter controlplane.EventEmitter,
	eventType controlplane.EventType,
	lease controllease.Lease,
) error {
	payload, err := json.Marshal(lease)
	if err != nil {
		return err
	}
	_, err = emitter.Emit(ctx, eventType, controlplane.Correlation{
		ActorID:   lease.Target.ActorID,
		SessionID: copyControlUUID(lease.Target.SessionID),
		TurnID:    copyControlUUID(lease.Owner.TurnID),
		TaskID:    copyControlUUID(lease.Owner.TaskID),
	}, payload)
	return err
}

func sameComputerOwner(
	owner controllease.Owner,
	input computerOwnerInput,
) bool {
	return sameControlUUID(owner.TurnID, input.TurnID) &&
		sameControlUUID(owner.TaskID, input.TaskID) &&
		strings.TrimSpace(owner.AgentID) == strings.TrimSpace(input.AgentID) &&
		sameControlUUID(owner.ToolEventID, input.ToolEventID) &&
		strings.TrimSpace(owner.Action) == strings.TrimSpace(input.Action) &&
		owner.Revision == input.Revision
}

func computerControlPublicError(err error) error {
	switch {
	case errors.Is(err, controllease.ErrStale):
		return controlplane.PublicError{
			Code:    controlplane.ErrorConflict,
			Message: "the control lease revision is stale",
		}
	case errors.Is(err, controllease.ErrHeld), errors.Is(err, controllease.ErrConflict):
		return controlplane.PublicError{
			Code:    controlplane.ErrorConflict,
			Message: "the resource authority changed before this action",
		}
	case errors.Is(err, controllease.ErrNotFound):
		return controlplane.PublicError{
			Code:    controlplane.ErrorNotFound,
			Message: "the control lease was not found",
		}
	case errors.Is(err, controllease.ErrUnauthorized):
		return controlplane.PublicError{
			Code:    controlplane.ErrorUnauthorized,
			Message: "the control lease is not authorized",
		}
	default:
		return err
	}
}

func copyControlUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func sameControlUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
