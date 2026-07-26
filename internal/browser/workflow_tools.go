package browser

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
)

func (supervisor *Supervisor) Registrations() []tools.Registration {
	check := func(context.Context) error {
		return supervisor.browser.Ready()
	}
	available := func(context.Context) error {
		return nil
	}
	workflowID := func(raw string) (uuid.UUID, error) {
		return uuid.Parse(raw)
	}
	return []tools.Registration{
		{
			Name: "browser_navigate", Description: "Start a supervised isolated browser workflow and return its bounded semantic page preview.",
			Parameters: json.RawMessage(`{"type":"object","required":["url"],"properties":{"url":{"type":"string","minLength":1,"maxLength":4096}},"additionalProperties":false}`),
			Timeout:    35 * time.Second, Check: check, Classification: tools.ClassificationYellow, ExternallyCommunicating: true,
			Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					URL string `json:"url"`
				}
				if err := decodeToolInput(raw, &input); err != nil {
					return nil, err
				}
				workflow, err := supervisor.Start(ctx, input.URL)
				return marshalWorkflowPreview(workflow, err)
			},
		},
		{
			Name: "browser_observe", Description: "Read the active supervised browser workflow as untrusted semantic evidence.",
			Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`),
			Timeout:    20 * time.Second, Check: check, Classification: tools.ClassificationGreen,
			Handler: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
				workflow, err := supervisor.Latest(ctx)
				if err == nil {
					workflow, err = supervisor.Observe(ctx, workflow.ID)
				}
				return marshalWorkflowPreview(workflow, err)
			},
		},
		{
			Name: "browser_interact", Description: "Perform one reversible action in the active supervised browser workflow.",
			Parameters: json.RawMessage(`{"type":"object","required":["action","ref"],"properties":{"action":{"type":"string","enum":["click","fill"]},"ref":{"type":"string","pattern":"^p[0-9]{1,12}$"},"value":{"type":"string","maxLength":16384}},"additionalProperties":false}`),
			Timeout:    25 * time.Second, Check: check, Classification: tools.ClassificationYellow, ExternallyCommunicating: true,
			Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					Action string `json:"action"`
					Ref    string `json:"ref"`
					Value  string `json:"value"`
				}
				if err := decodeToolInput(raw, &input); err != nil {
					return nil, err
				}
				workflow, err := supervisor.Latest(ctx)
				if err == nil {
					workflow, err = supervisor.Interact(ctx, workflow.ID, input.Action, input.Ref, input.Value)
				}
				return marshalWorkflowPreview(workflow, err)
			},
		},
		{
			Name: "browser_submit", Description: "Activate one consequential browser control in the active supervised workflow after exact approval.",
			Parameters: json.RawMessage(`{"type":"object","required":["ref"],"properties":{"ref":{"type":"string","pattern":"^p[0-9]{1,12}$"}},"additionalProperties":false}`),
			Timeout:    35 * time.Second, Check: check, Classification: tools.ClassificationRed, ExternallyCommunicating: true,
			Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					Ref string `json:"ref"`
				}
				if err := decodeToolInput(raw, &input); err != nil {
					return nil, err
				}
				workflow, err := supervisor.Latest(ctx)
				if err == nil {
					workflow, err = supervisor.Submit(ctx, workflow.ID, input.Ref)
				}
				return marshalWorkflowPreview(workflow, err)
			},
		},
		{
			Name: "browser_workflow_status", Description: "List supervised browser workflows and their live semantic previews.",
			Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`),
			Timeout:    10 * time.Second, Check: available, Classification: tools.ClassificationGreen,
			Handler: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
				workflows, err := supervisor.List(ctx)
				if err != nil {
					return nil, err
				}
				return json.Marshal(workflows)
			},
		},
		workflowTransitionRegistration("browser_workflow_pause", "Pause a supervised browser workflow.", tools.ClassificationYellow, available, supervisor.Pause, workflowID),
		workflowTransitionRegistration("browser_workflow_resume", "Resume a paused supervised browser workflow.", tools.ClassificationYellow, available, supervisor.Resume, workflowID),
		workflowTransitionRegistration("browser_workflow_cancel", "Cancel a supervised browser workflow and clear its volatile browser profile.", tools.ClassificationYellow, available, supervisor.Cancel, workflowID),
		{
			Name: "browser_request_handoff", Description: "Pause a browser workflow for a typed human-only action and state the consequence.",
			Parameters: json.RawMessage(`{"type":"object","required":["workflow_id","kind","consequence"],"properties":{"workflow_id":{"type":"string","format":"uuid"},"kind":{"type":"string","enum":["captcha","passkey","legal_identity","terms","payment","recovery","ambiguous_control"]},"consequence":{"type":"string","minLength":1,"maxLength":500}},"additionalProperties":false}`),
			Timeout:    10 * time.Second, Check: available, Classification: tools.ClassificationYellow,
			Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					WorkflowID  string      `json:"workflow_id"`
					Kind        HandoffKind `json:"kind"`
					Consequence string      `json:"consequence"`
				}
				if err := decodeToolInput(raw, &input); err != nil {
					return nil, err
				}
				id, err := workflowID(input.WorkflowID)
				if err != nil {
					return nil, err
				}
				workflow, err := supervisor.RequestHandoff(ctx, id, input.Kind, input.Consequence)
				return marshalWorkflow(workflow, err)
			},
		},
		{
			Name: "browser_apply_credential", Description: "Insert an origin-bound Vault credential reference during a paused human handoff without exposing its secret.",
			Parameters: json.RawMessage(`{"type":"object","required":["workflow_id","credential_id","ref"],"properties":{"workflow_id":{"type":"string","format":"uuid"},"credential_id":{"type":"string","format":"uuid"},"ref":{"type":"string","pattern":"^p[0-9]{1,4}$"}},"additionalProperties":false}`),
			Timeout:    20 * time.Second, Check: check, Classification: tools.ClassificationRed,
			Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var input struct {
					WorkflowID   string `json:"workflow_id"`
					CredentialID string `json:"credential_id"`
					Ref          string `json:"ref"`
				}
				if err := decodeToolInput(raw, &input); err != nil {
					return nil, err
				}
				id, err := workflowID(input.WorkflowID)
				if err != nil {
					return nil, err
				}
				credentialID, err := workflowID(input.CredentialID)
				if err != nil {
					return nil, err
				}
				workflow, err := supervisor.InsertCredential(ctx, id, credentialID, input.Ref)
				return marshalWorkflow(workflow, err)
			},
		},
	}
}

func workflowTransitionRegistration(
	name string,
	description string,
	classification tools.Classification,
	check func(context.Context) error,
	transition func(context.Context, uuid.UUID) (Workflow, error),
	parse func(string) (uuid.UUID, error),
) tools.Registration {
	return tools.Registration{
		Name: name, Description: description,
		Parameters: json.RawMessage(`{"type":"object","required":["workflow_id"],"properties":{"workflow_id":{"type":"string","format":"uuid"}},"additionalProperties":false}`),
		Timeout:    10 * time.Second, Check: check, Classification: classification,
		Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				WorkflowID string `json:"workflow_id"`
			}
			if err := decodeToolInput(raw, &input); err != nil {
				return nil, err
			}
			id, err := parse(input.WorkflowID)
			if err != nil {
				return nil, err
			}
			workflow, err := transition(ctx, id)
			return marshalWorkflow(workflow, err)
		},
	}
}

func marshalWorkflowPreview(workflow Workflow, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return json.Marshal(workflow.Preview)
}

func marshalWorkflow(workflow Workflow, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return json.Marshal(workflow)
}
