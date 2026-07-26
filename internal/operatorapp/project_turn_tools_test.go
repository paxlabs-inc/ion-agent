package operatorapp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

func TestStudioTurnToolsBindMutationsPlanAndIndexToRegisteredProject(t *testing.T) {
	ctx := context.Background()
	dataRoot, generalWorkspace := t.TempDir(), t.TempDir()
	initializeRuntimeVault(t, ctx, dataRoot)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataRoot, DevelopmentFileKEK: true,
		WorkspaceDirectory: generalWorkspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	actorID, sessionID := uuid.New(), uuid.New()
	project, err := runtime.capabilityRoot.projects.CreateTemplate(
		ctx,
		projectcontrol.OperationMeta{
			ActorID: actorID, RequestID: uuid.New(), IdempotencyKey: "bound-project",
			PolicyClassification: projectcontrol.PolicyYellow,
			Deadline:             time.Now().Add(time.Minute), CorrelationID: uuid.New(),
		},
		projectcontrol.TemplateInput{
			Name: "Bound app", Template: "empty", Host: projectcontrol.HostDirectLocal,
			Trust: projectcontrol.TrustReviewed,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, bound, err := runtime.capabilityRoot.projectToolsForMessages(
		ctx, actorID, []protocol.Message{{
			Role:    protocol.RoleUser,
			Content: "Continue this Software Studio project (" + project.ID.String() + ") and build it.",
		}},
	)
	if err != nil || bound == nil || bound.ID != project.ID {
		t.Fatalf("bound project = %+v, %v", bound, err)
	}
	hasPlanTool := false
	hasGenericContractTool := false
	var planParameters json.RawMessage
	var planDescription string
	for _, definition := range manager.Surface(ctx) {
		if definition.Name == "studio_plan_propose" {
			hasPlanTool = true
			planParameters = definition.Parameters
			planDescription = definition.Description
		}
		hasGenericContractTool = hasGenericContractTool || definition.Name == "work_contract_set"
	}
	if !hasPlanTool {
		t.Fatal("bound Studio plan tool is missing")
	}
	if hasGenericContractTool {
		t.Fatal("bound Studio surface exposed the generic contract bypass")
	}
	if strings.Contains(string(planParameters), `"done_criteria"`) ||
		!strings.Contains(planDescription, "single criterion source") {
		t.Fatalf("Studio plan contract still advertises duplicate criteria: %s %s",
			planDescription, planParameters)
	}
	prompt := studioProjectPrompt(project)
	if !strings.Contains(prompt, "Registered absolute workspace root: "+project.Root) ||
		!strings.Contains(prompt, "Report this exact absolute root before the first edit") {
		t.Fatalf("project prompt does not disclose the registered root: %s", prompt)
	}
	runCtx := controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
		ActorID: actorID, SessionID: &sessionID,
	})
	if _, err := manager.Execute(runCtx, protocol.NormalizedToolCall{
		ID: "reject-write-before-plan", Name: "filesystem_write",
		Arguments: json.RawMessage(`{"path":"package.json","content":"{\"scripts\":{\"dev\":\"next dev\"}}\n"}`),
	}); err == nil || !strings.Contains(err.Error(), "studio_plan_propose") {
		t.Fatalf("workspace mutation before a Studio plan = %v", err)
	}
	if _, err := os.Stat(filepath.Join(project.Root, "package.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-plan write changed the project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(generalWorkspace, "package.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("write escaped into general workspace: %v", err)
	}
	invalidPlan := json.RawMessage(`{
		"goal":"Build a working app","deliverable":"A verified app",
		"verification_required":["npm test"],"next_action":"Build it",
		"rationale":"The project needs a plan",
		"spec_delta":{"user_visible_behavior":["The app loads"],
			"acceptance_criteria":[{"id":"app.runs","description":"The app runs"}],
			"security_boundaries":["Project files only"],"data_boundaries":["No production data"],
			"migration":[],"rollback":[],"verification_commands":["npm test"]}
	}`)
	if _, err := manager.Execute(runCtx, protocol.NormalizedToolCall{
		ID: "reject-missing-tasks", Name: "studio_plan_propose", Arguments: invalidPlan,
	}); err == nil {
		t.Fatal("plan without spec_delta.tasks succeeded")
	} else {
		var validation *tools.ArgumentValidationError
		if !errors.As(err, &validation) || len(validation.Issues) == 0 ||
			validation.Issues[0].Path != "spec_delta" {
			t.Fatalf("structured plan validation = %#v, %v", validation, err)
		}
	}
	briefBeforePlan, err := runtime.capabilityRoot.work.Brief(ctx, actorID, &sessionID)
	if err != nil || briefBeforePlan.Contract != nil {
		t.Fatalf("rejected plan left a partial Work Brief: %+v, %v", briefBeforePlan, err)
	}
	for _, testCase := range []struct {
		name      string
		arguments string
		path      string
	}{
		{name: "criterion strings", path: "spec_delta.acceptance_criteria.0", arguments: `{
			"goal":"Build","deliverable":"App",
			"verification_required":["npm test"],"next_action":"Build","rationale":"Plan",
			"spec_delta":{"user_visible_behavior":["Loads"],"acceptance_criteria":["Runs"],
			"security_boundaries":["Project"],"data_boundaries":["Local"],"migration":[],"rollback":[],
			"verification_commands":["npm test"],"tasks":[{"id":"build","title":"Build","criteria":["runs"]}]}}`},
		{name: "migration string", path: "spec_delta.migration", arguments: `{
			"goal":"Build","deliverable":"App",
			"verification_required":["npm test"],"next_action":"Build","rationale":"Plan",
			"spec_delta":{"user_visible_behavior":["Loads"],"acceptance_criteria":[{"id":"runs","description":"Runs"}],
			"security_boundaries":["Project"],"data_boundaries":["Local"],"migration":"none","rollback":[],
			"verification_commands":["npm test"],"tasks":[{"id":"build","title":"Build","criteria":["runs"]}]}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := tools.ValidateArguments(planParameters, json.RawMessage(testCase.arguments))
			var validation *tools.ArgumentValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("schema validation = %#v, %v", validation, err)
			}
			found := false
			for _, issue := range validation.Issues {
				found = found || issue.Path == testCase.path
			}
			if !found {
				t.Fatalf("schema issues = %+v, want path %s", validation.Issues, testCase.path)
			}
		})
	}
	rollbackPlan := json.RawMessage(`{
		"goal":"Build a working app","deliverable":"A verified app",
		"verification_required":["npm test"],"next_action":"Build it",
		"rationale":"The project needs a plan",
		"assumptions":[{"id":"material-choice","statement":"A material choice is unresolved","reversible":false,"material":true}],
		"spec_delta":{"user_visible_behavior":["The app loads"],
			"acceptance_criteria":[{"id":"app.runs","description":"The app runs"}],
			"security_boundaries":["Project files only"],"data_boundaries":["No production data"],
			"migration":[],"rollback":[],"verification_commands":["npm test"],
			"tasks":[{"id":"app.build","title":"Build the app","criteria":["app.runs"]}]}
	}`)
	if _, err := manager.Execute(runCtx, protocol.NormalizedToolCall{
		ID: "rollback-after-studio-failure", Name: "studio_plan_propose", Arguments: rollbackPlan,
	}); err == nil {
		t.Fatal("material assumption without a decision unexpectedly compiled")
	}
	briefAfterCompensation, err := runtime.capabilityRoot.work.Brief(ctx, actorID, &sessionID)
	if err != nil || briefAfterCompensation.Contract != nil {
		t.Fatalf("Studio failure was not compensated: %+v, %v", briefAfterCompensation, err)
	}
	intentsBeforeValid, _, err := runtime.capabilityRoot.studio.List(ctx, actorID)
	if err != nil || len(intentsBeforeValid) != 0 {
		t.Fatalf("failed plan left Studio state: %+v, %v", intentsBeforeValid, err)
	}
	plan := json.RawMessage(`{
		"goal":"Build a working app",
		"deliverable":"A verified app in the registered workspace",
		"verification_required":["The test suite must pass"],
		"next_action":"Review the proposed plan",
		"rationale":"The project has no existing implementation plan",
		"spec_delta":{
			"user_visible_behavior":["The app loads"],
			"non_goals":[],"constraints":["Keep work in the project root"],
			"risks":["Dependency installation may fail"],
			"acceptance_criteria":[{"id":"app.runs","description":"The app runs"}],
			"security_boundaries":["Project files only"],
			"data_boundaries":["No production data"],"migration":[],
			"rollback":["Restore the prior project revision"],
			"verification_commands":["npm test"],
			"tasks":[{"id":"app.build","title":"Build the app","criteria":["app.runs"]}]
		}
	}`)
	if _, err := manager.Execute(runCtx, protocol.NormalizedToolCall{
		ID: "propose-plan", Name: "studio_plan_propose", Arguments: plan,
	}); err != nil {
		t.Fatal(err)
	}
	intents, _, err := runtime.capabilityRoot.studio.List(ctx, actorID)
	if err != nil || len(intents) != 1 || intents[0].ProjectID != project.ID ||
		len(intents[0].Proposals) != 1 {
		t.Fatalf("Studio intents = %+v, %v", intents, err)
	}
	brief, err := runtime.capabilityRoot.work.Brief(ctx, actorID, &sessionID)
	if err != nil || brief.Contract == nil || brief.Contract.Goal != "Build a working app" ||
		len(brief.Contract.DoneCriteria) != 1 ||
		brief.Contract.DoneCriteria[0].ID != "app.runs" ||
		brief.Contract.DoneCriteria[0].Description != "The app runs" {
		t.Fatalf("Work Brief = %+v, %v", brief, err)
	}
	if _, err := manager.Execute(runCtx, protocol.NormalizedToolCall{
		ID: "write-package-after-plan", Name: "filesystem_write",
		Arguments: json.RawMessage(`{"path":"package.json","content":"{\"scripts\":{\"dev\":\"next dev\"}}\n"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project.Root, "package.json")); err != nil {
		t.Fatalf("project-root write missing after plan: %v", err)
	}
	if _, err := manager.Execute(runCtx, protocol.NormalizedToolCall{
		ID: "write-project-artifact", Name: "filesystem_write",
		Arguments: json.RawMessage(`{"path":"index.html","content":"<!doctype html><title>Bound artifact</title>\n"}`),
	}); err != nil {
		t.Fatal(err)
	}
	recordArguments, err := json.Marshal(map[string]any{
		"contract_id": brief.Contract.ID, "kind": "web_page",
		"title": "Project page", "reference": "index.html",
		"criteria_covered": []string{"app.runs"},
	})
	if err != nil {
		t.Fatal(err)
	}
	recordedResult, err := manager.Execute(runCtx, protocol.NormalizedToolCall{
		ID: "record-project-artifact", Name: "artifact_record", Arguments: recordArguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	var recorded workcontrol.Artifact
	if err := json.Unmarshal(recordedResult, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Reference != "index.html" || recorded.WorkspaceRoot != project.Root {
		t.Fatalf("project artifact binding = %+v", recorded)
	}
	verifyArguments, err := json.Marshal(map[string]any{"artifact_id": recorded.ID})
	if err != nil {
		t.Fatal(err)
	}
	verifiedResult, err := manager.Execute(runCtx, protocol.NormalizedToolCall{
		ID: "verify-project-artifact", Name: "artifact_verify", Arguments: verifyArguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	var verified workcontrol.Artifact
	if err := json.Unmarshal(verifiedResult, &verified); err != nil {
		t.Fatal(err)
	}
	if verified.SHA256 == "" || verified.VerifiedAt == nil ||
		verified.WorkspaceRoot != project.Root {
		t.Fatalf("verified project artifact = %+v", verified)
	}
	if _, err := os.Stat(filepath.Join(generalWorkspace, "index.html")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact verification used the general workspace: %v", err)
	}
	updated, err := runtime.capabilityRoot.projects.Get(ctx, actorID, project.ID)
	if err != nil || updated.WorkspaceRevision != project.WorkspaceRevision+2 ||
		len(updated.StackSignals) != 1 || updated.StackSignals[0] != "node" {
		t.Fatalf("updated project = %+v, %v", updated, err)
	}
	index, err := runtime.capabilityRoot.projects.ProjectIndex(ctx, actorID, project.ID)
	if err != nil || index.WorkspaceRevision != updated.WorkspaceRevision ||
		len(index.Files) == 0 {
		t.Fatalf("fresh project index = %+v, %v", index, err)
	}
}

func TestStructuredTurnSurfaceOverridesStaleTranscriptBinding(t *testing.T) {
	ctx := context.Background()
	dataRoot, generalWorkspace := t.TempDir(), t.TempDir()
	initializeRuntimeVault(t, ctx, dataRoot)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataRoot, DevelopmentFileKEK: true,
		WorkspaceDirectory: generalWorkspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	actorID := uuid.New()
	project, err := runtime.capabilityRoot.projects.CreateTemplate(
		ctx,
		projectcontrol.OperationMeta{
			ActorID: actorID, RequestID: uuid.New(), IdempotencyKey: "structured-binding",
			PolicyClassification: projectcontrol.PolicyYellow,
			Deadline:             time.Now().Add(time.Minute), CorrelationID: uuid.New(),
		},
		projectcontrol.TemplateInput{
			Name: "Safe app", Template: "empty", Host: projectcontrol.HostDirectLocal,
			Trust: projectcontrol.TrustReviewed,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	staleMessages := []protocol.Message{{
		Role:    protocol.RoleUser,
		Content: "Continue this Software Studio project (" + project.ID.String() + ")",
	}}
	general, bound, err := runtime.capabilityRoot.projectToolsForTurn(
		ctx, actorID, "general", uuid.Nil, staleMessages,
	)
	if err != nil || bound != nil || general == nil {
		t.Fatalf("general binding = %+v, %v", bound, err)
	}
	for _, definition := range general.Surface(ctx) {
		if definition.Name == "studio_plan_propose" {
			t.Fatal("general turn inherited stale Studio tools")
		}
	}
	studio, bound, err := runtime.capabilityRoot.projectToolsForTurn(
		ctx, actorID, "studio", project.ID,
		[]protocol.Message{{Role: protocol.RoleUser, Content: "Build the requested app"}},
	)
	if err != nil || bound == nil || bound.ID != project.ID || studio == nil {
		t.Fatalf("Studio binding = %+v, %v", bound, err)
	}
	foundPlan := false
	for _, definition := range studio.Surface(ctx) {
		if definition.Name == "studio_plan_propose" {
			foundPlan = true
		}
	}
	if !foundPlan {
		t.Fatal("structured Studio turn did not receive project tools")
	}
}
