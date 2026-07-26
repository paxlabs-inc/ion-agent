package operatorapp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
)

const supervisorStartToolSchema = `{
	"type":"object",
	"additionalProperties":false,
	"required":["contract_id"],
	"properties":{
		"contract_id":{"type":"string","format":"uuid"},
		"project_id":{"type":"string","format":"uuid"},
		"budget":{"$ref":"#/$defs/supervisor_budget"},
		"project_budget":{"$ref":"#/$defs/supervisor_budget"},
		"overrides":{
			"type":"array",
			"maxItems":32,
			"items":{
				"type":"object",
				"additionalProperties":false,
				"required":["work_item_id","specialist"],
				"properties":{
					"work_item_id":{"type":"string","minLength":1,"maxLength":128},
					"specialist":{"enum":["discovery","exploration","implementation","test","security","data","frontend","performance","operations","review"]},
					"scope":{"$ref":"#/$defs/authority_scope"},
					"tools":{"type":"array","maxItems":32,"items":{"type":"string","minLength":1,"maxLength":128}},
					"budget":{"$ref":"#/$defs/specialist_budget"}
				}
			}
		}
	},
	"$defs":{
		"specialist_budget":{
			"type":"object",
			"additionalProperties":false,
			"properties":{
				"max_tokens":{"type":"integer","minimum":1},
				"max_cost_cents":{"type":"integer","minimum":0},
				"max_tool_calls":{"type":"integer","minimum":1,"maximum":32},
				"max_wall_seconds":{"type":"integer","minimum":1},
				"max_processes":{"type":"integer","minimum":1},
				"max_storage_bytes":{"type":"integer","minimum":0},
				"max_network_bytes":{"type":"integer","minimum":0},
				"max_provider_cents":{"type":"integer","minimum":0},
				"max_retries":{"type":"integer","minimum":0,"maximum":10}
			}
		},
		"supervisor_budget":{
			"type":"object",
			"additionalProperties":false,
			"required":["max_parallel","max_tokens","max_cost_cents","max_tool_calls","max_wall_seconds","max_processes","max_storage_bytes","max_network_bytes","max_provider_cents","max_retries"],
			"properties":{
				"max_parallel":{"type":"integer","minimum":1,"maximum":32},
				"max_tokens":{"type":"integer","minimum":1},
				"max_cost_cents":{"type":"integer","minimum":0},
				"max_tool_calls":{"type":"integer","minimum":1},
				"max_wall_seconds":{"type":"integer","minimum":1},
				"max_processes":{"type":"integer","minimum":1},
				"max_storage_bytes":{"type":"integer","minimum":0},
				"max_network_bytes":{"type":"integer","minimum":0},
				"max_provider_cents":{"type":"integer","minimum":0},
				"max_retries":{"type":"integer","minimum":0,"maximum":10}
			}
		},
		"authority_scope":{
			"type":"object",
			"additionalProperties":false,
			"properties":{
				"read_files":{"type":"array","maxItems":64,"items":{"type":"string","minLength":1,"maxLength":4096}},
				"write_files":{"type":"array","maxItems":64,"items":{"type":"string","minLength":1,"maxLength":4096}},
				"services":{"type":"array","maxItems":32,"items":{"type":"string","minLength":1,"maxLength":256}},
				"environment_keys":{"type":"array","maxItems":32,"items":{"type":"string","minLength":1,"maxLength":256}},
				"network_hosts":{"type":"array","maxItems":32,"items":{"type":"string","minLength":1,"maxLength":512}},
				"external_effects":{"type":"boolean"}
			}
		}
	}
}`

func (capabilities *productionCapabilities) registerWorkTools(ctx context.Context) error {
	if capabilities.work == nil {
		return fmt.Errorf("operator capabilities: disciplined work service is required")
	}
	registrations := []tools.Registration{
		workRegistration("work_brief",
			"Read the current outcome, next action, verification coverage, deliverables, and autonomy limits.",
			`{"type":"object","additionalProperties":false}`,
			tools.ClassificationGreen,
			func(runCtx context.Context, _ json.RawMessage) (json.RawMessage, error) {
				actor, sessionID, err := authenticatedWorkScope(runCtx)
				if err != nil {
					return nil, err
				}
				brief, err := capabilities.work.Brief(runCtx, actor, sessionID)
				return marshalWorkResult(brief, err)
			}),
		workRegistration("supervisor_status",
			"Read the durable specialist workstreams, live progress, attempts, budgets, leases, and restart reconciliation for the current actor.",
			`{"type":"object","additionalProperties":false,"properties":{"run_id":{"type":"string","format":"uuid"}}}`,
			tools.ClassificationGreen,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, _, err := authenticatedWorkScope(runCtx)
				if err != nil {
					return nil, err
				}
				if capabilities.supervisor == nil {
					return nil, fmt.Errorf("operator capabilities: supervisor is unavailable")
				}
				var input struct {
					RunID uuid.UUID `json:"run_id"`
				}
				if err := decodeStrictJSON(raw, &input); err != nil {
					return nil, err
				}
				run, err := capabilities.supervisor.Get(runCtx, actor, input.RunID)
				return marshalWorkResult(run, err)
			}),
		workRegistration("supervisor_start",
			"Compile an accepted outcome contract into one durable dependency supervisor and dispatch only ready specialist work within hard budgets.",
			supervisorStartToolSchema,
			tools.ClassificationYellow,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, sessionID, err := authenticatedWorkScope(runCtx)
				if err != nil {
					return nil, err
				}
				if capabilities.supervisor == nil {
					return nil, fmt.Errorf("operator capabilities: supervisor is unavailable")
				}
				var input workcontrol.SupervisorStartInput
				if err := decodeStrictJSON(raw, &input); err != nil {
					return nil, err
				}
				input.SessionID = sessionID
				run, err := capabilities.supervisor.Start(runCtx, actor, input)
				return marshalWorkResult(run, err)
			}),
		workRegistration("supervisor_steer",
			"Add one bounded parent instruction to an active supervisor without expanding specialist authority.",
			`{"type":"object","additionalProperties":false,"required":["run_id","instruction"],"properties":{"run_id":{"type":"string","format":"uuid"},"instruction":{"type":"string","minLength":1,"maxLength":4096}}}`,
			tools.ClassificationYellow,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, _, err := authenticatedWorkScope(runCtx)
				if err != nil {
					return nil, err
				}
				var input struct {
					RunID       uuid.UUID `json:"run_id"`
					Instruction string    `json:"instruction"`
				}
				if err := decodeStrictJSON(raw, &input); err != nil {
					return nil, err
				}
				run, err := capabilities.supervisor.Steer(
					runCtx, actor, input.RunID, input.Instruction,
				)
				return marshalWorkResult(run, err)
			}),
		workRegistration("supervisor_cancel",
			"Cancel an active supervisor, propagate cancellation to every child, and release all scopes.",
			`{"type":"object","additionalProperties":false,"required":["run_id"],"properties":{"run_id":{"type":"string","format":"uuid"}}}`,
			tools.ClassificationYellow,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, _, err := authenticatedWorkScope(runCtx)
				if err != nil {
					return nil, err
				}
				var input struct {
					RunID uuid.UUID `json:"run_id"`
				}
				if err := decodeStrictJSON(raw, &input); err != nil {
					return nil, err
				}
				run, err := capabilities.supervisor.Cancel(runCtx, actor, input.RunID)
				return marshalWorkResult(run, err)
			}),
		workRegistration("work_contract_set",
			"Create or update the durable definition of success for substantial work. Completion is not accepted here.",
			`{"type":"object","additionalProperties":false,"required":["goal","deliverable","done_criteria","verification_required","next_action"],"properties":{"id":{"type":"string","format":"uuid"},"goal":{"type":"string","minLength":1,"maxLength":4096},"deliverable":{"type":"string","minLength":1,"maxLength":4096},"done_criteria":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"object","additionalProperties":false,"required":["id","description"],"properties":{"id":{"type":"string","minLength":1,"maxLength":128},"description":{"type":"string","minLength":1,"maxLength":4096}}}},"verification_required":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"string","minLength":1,"maxLength":4096}},"next_action":{"type":"string","minLength":1,"maxLength":4096},"status":{"enum":["draft","active","blocked","cancelled"]}}}`,
			tools.ClassificationYellow,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, sessionID, err := authenticatedWorkScope(runCtx)
				if err != nil {
					return nil, err
				}
				var input workcontrol.ContractInput
				if err := decodeStrictJSON(raw, &input); err != nil {
					return nil, err
				}
				input.SessionID = sessionID
				contract, err := capabilities.work.PutContract(runCtx, actor, input)
				return marshalWorkResult(contract, err)
			}),
		workRegistration("artifact_record",
			"Register a workspace-relative deliverable as unverified evidence for completion criteria.",
			`{"type":"object","additionalProperties":false,"required":["contract_id","kind","title","reference","criteria_covered"],"properties":{"contract_id":{"type":"string","format":"uuid"},"kind":{"type":"string","minLength":1,"maxLength":4096},"title":{"type":"string","minLength":1,"maxLength":4096},"reference":{"type":"string","minLength":1,"maxLength":4096},"criteria_covered":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"string","minLength":1,"maxLength":128}}}}`,
			tools.ClassificationYellow,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, _, err := authenticatedWorkScope(runCtx)
				if err != nil {
					return nil, err
				}
				var input workcontrol.ArtifactInput
				if err := decodeStrictJSON(raw, &input); err != nil {
					return nil, err
				}
				artifact, err := capabilities.work.RecordArtifact(runCtx, actor, input)
				return marshalWorkResult(artifact, err)
			}),
		workRegistration("work_item_update",
			"Advance one durable criterion-linked work item. Dependencies and verified evidence are enforced by the runtime.",
			`{"type":"object","additionalProperties":false,"required":["contract_id","item_id","status"],"properties":{"contract_id":{"type":"string","format":"uuid"},"item_id":{"type":"string","minLength":1,"maxLength":128},"status":{"enum":["running","verifying","blocked","completed"]},"note":{"type":"string","maxLength":4096}}}`,
			tools.ClassificationYellow,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, _, err := authenticatedWorkScope(runCtx)
				if err != nil {
					return nil, err
				}
				var input workcontrol.WorkItemUpdate
				if err := decodeStrictJSON(raw, &input); err != nil {
					return nil, err
				}
				item, err := capabilities.work.UpdateWorkItem(runCtx, actor, input)
				return marshalWorkResult(item, err)
			}),
		workRegistration("artifact_verify",
			"Independently read and hash a registered workspace deliverable. The client cannot supply the digest.",
			`{"type":"object","additionalProperties":false,"required":["artifact_id"],"properties":{"artifact_id":{"type":"string","format":"uuid"}}}`,
			tools.ClassificationYellow,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, _, err := authenticatedWorkScope(runCtx)
				if err != nil {
					return nil, err
				}
				var input struct {
					ArtifactID uuid.UUID `json:"artifact_id"`
				}
				if err := decodeStrictJSON(raw, &input); err != nil || input.ArtifactID == uuid.Nil {
					return nil, fmt.Errorf("operator capabilities: valid artifact_id is required")
				}
				artifact, err := capabilities.work.VerifyArtifact(runCtx, actor, input.ArtifactID)
				return marshalWorkResult(artifact, err)
			}),
		workRegistration("work_contract_complete",
			"Complete an outcome contract only if server-verified artifacts cover every done criterion.",
			`{"type":"object","additionalProperties":false,"required":["contract_id"],"properties":{"contract_id":{"type":"string","format":"uuid"}}}`,
			tools.ClassificationYellow,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, _, err := authenticatedWorkScope(runCtx)
				if err != nil {
					return nil, err
				}
				var input struct {
					ContractID uuid.UUID `json:"contract_id"`
				}
				if err := decodeStrictJSON(raw, &input); err != nil || input.ContractID == uuid.Nil {
					return nil, fmt.Errorf("operator capabilities: valid contract_id is required")
				}
				contract, err := capabilities.work.CompleteContract(runCtx, actor, input.ContractID)
				return marshalWorkResult(contract, err)
			}),
	}
	for _, registration := range registrations {
		if err := capabilities.manager.Register(ctx, registration); err != nil {
			return fmt.Errorf("operator capabilities: register %s: %w", registration.Name, err)
		}
	}
	return nil
}

func workRegistration(name, description, schema string, classification tools.Classification, handler tools.Handler) tools.Registration {
	return tools.Registration{Name: name, Description: description,
		Parameters: json.RawMessage(schema), Timeout: 30 * time.Second,
		Classification: classification, Handler: handler,
		Check: func(context.Context) error { return nil }}
}

func authenticatedWorkScope(ctx context.Context) (uuid.UUID, *uuid.UUID, error) {
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok || scope.ActorID == uuid.Nil {
		return uuid.Nil, nil, fmt.Errorf("operator capabilities: authenticated work scope is required")
	}
	return scope.ActorID, scope.SessionID, nil
}

func marshalWorkResult(value any, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	raw, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return nil, fmt.Errorf("operator capabilities: encode work result: %w", marshalErr)
	}
	return raw, nil
}
