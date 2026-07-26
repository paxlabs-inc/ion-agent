package operatorapp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
	studiocontrol "github.com/paxlabs-inc/ion-agent/internal/studio"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
)

func TestStudioProductionJourneysRestartDriftAndFalseCompletion(t *testing.T) {
	ctx := context.Background()
	dataRoot, projectRoot, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	initializeRuntimeVault(t, ctx, dataRoot)
	config := RuntimeConfig{DataDirectory: dataRoot, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace, ProjectWorkspaceRoot: projectRoot}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	actor, other := uuid.New(), uuid.New()
	created := dispatchStudioProject(t, ctx, runtime, actor, controlplane.OperationProjectCreate,
		"greenfield-project", map[string]any{"name": "Greenfield", "template": "empty", "host": "direct_local"})
	if err := os.MkdirAll(filepath.Join(created.Root, "spec"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"AGENTS.md":     "Keep user-visible behavior accessible.\n",
		"spec/spec.kvx": "# authoritative\n", "spec/tasks.md": "# Tasks\n", "main.go": "package main\n",
	} {
		if err := os.WriteFile(filepath.Join(created.Root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	contract := dispatchContract(t, ctx, runtime, actor, "greenfield-contract", "Build a status page")
	delta := productionStudioDelta()
	intentResponse := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationStudioIntentCompile,
		"compile-greenfield", studiocontrol.CompileInput{ProjectID: created.ID,
			OutcomeContractID: contract.ID, WorkspaceRevision: created.WorkspaceRevision,
			Goal: contract.Goal, Rationale: "No existing requirement covers the requested page", Delta: &delta,
			Assumptions: []studiocontrol.Assumption{{ID: "route", Statement: "Use the existing router", Reversible: true}}})
	var intent studiocontrol.Intent
	decodeStudioResult(t, intentResponse, &intent)
	if len(intent.Proposals) != 1 || intent.Inspection.InstructionFiles[0] != "AGENTS.md" {
		t.Fatalf("compiled production intent = %+v", intent)
	}
	otherList := studioQuery(t, ctx, runtime, other, controlplane.OperationStudioIntentList, map[string]any{})
	if !bytes.Contains(otherList.Result, []byte(`"intents":[]`)) {
		t.Fatalf("cross-actor Studio state = %s", otherList.Result)
	}
	dispatchStudio(t, ctx, runtime, actor, controlplane.OperationStudioProposalDecide,
		"reject-initial", map[string]any{"intent_id": intent.ID, "proposal_id": intent.Proposals[0].ID,
			"accept": false, "reason": "User rejected the initial scope"})
	scope := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationStudioScopePropose,
		"scope-change", studiocontrol.ScopeChangeInput{IntentID: intent.ID,
			Rationale:        "Brownfield inspection found an authentication dependency",
			DependencyImpact: []string{"authentication precedes the route"}, Delta: delta})
	var proposal studiocontrol.Proposal
	decodeStudioResult(t, scope, &proposal)
	dispatchStudio(t, ctx, runtime, actor, controlplane.OperationStudioProposalDecide,
		"accept-scope", map[string]any{"intent_id": intent.ID, "proposal_id": proposal.ID,
			"accept": true, "reason": "User approved the revised scope"})
	appliedResponse := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationStudioProposalApply,
		"apply-scope", map[string]any{"intent_id": intent.ID, "proposal_id": proposal.ID})
	var applied studiocontrol.Intent
	decodeStudioResult(t, appliedResponse, &applied)
	if applied.Proposals[len(applied.Proposals)-1].AppliedAt == nil {
		t.Fatal("accepted proposal was not applied")
	}
	if data, err := os.ReadFile(filepath.Join(created.Root, "spec", "spec.kvx")); err != nil ||
		!bytes.Contains(data, []byte("ION STUDIO CHANGE")) {
		t.Fatalf("authoritative KVX = %s, %v", data, err)
	}
	blocked := studioQuery(t, ctx, runtime, actor, controlplane.OperationStudioCompletionCheck,
		map[string]any{"intent_id": intent.ID})
	var incomplete studiocontrol.Completion
	decodeStudioResult(t, blocked, &incomplete)
	if incomplete.Ready || len(incomplete.BlockingReasons) == 0 {
		t.Fatalf("false completion = %+v", incomplete)
	}
	if err := os.WriteFile(filepath.Join(workspace, "studio-acceptance.txt"), []byte("verified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifactResponse := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationArtifactRecord,
		"record-studio-artifact", workcontrol.ArtifactInput{ContractID: contract.ID, Kind: "report",
			Title: "Studio acceptance", Reference: "studio-acceptance.txt", CriteriaCovered: []string{"status.visible"}})
	var artifact workcontrol.Artifact
	decodeStudioResult(t, artifactResponse, &artifact)
	verifiedResponse := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationArtifactVerify,
		"verify-studio-artifact", map[string]any{"artifact_id": artifact.ID})
	decodeStudioResult(t, verifiedResponse, &artifact)
	for index, correlation := range []studiocontrol.CorrelationInput{
		{IntentID: intent.ID, Kind: studiocontrol.CorrelationTask, Reference: "status.route", Criteria: []string{"status.visible"}, Description: "task"},
		{IntentID: intent.ID, Kind: studiocontrol.CorrelationPatch, Reference: "patch-1", Criteria: []string{"status.visible"}, Description: "patch"},
		{IntentID: intent.ID, Kind: studiocontrol.CorrelationTool, Reference: "tool-event-1", Criteria: []string{"status.visible"}, Description: "tool"},
		{IntentID: intent.ID, Kind: studiocontrol.CorrelationReview, Reference: "review-1", Criteria: []string{"status.visible"}, Description: "review"},
		{IntentID: intent.ID, Kind: studiocontrol.CorrelationVerification, Reference: "verification-1", Criteria: []string{"status.visible"}, Description: "verification"},
		{IntentID: intent.ID, Kind: studiocontrol.CorrelationArtifact, Reference: artifact.ID.String(), Criteria: []string{"status.visible"}, Description: "artifact"},
	} {
		dispatchStudio(t, ctx, runtime, actor, controlplane.OperationStudioCorrelationRecord,
			"correlation-"+string(rune('a'+index)), correlation)
	}
	readyResponse := studioQuery(t, ctx, runtime, actor, controlplane.OperationStudioCompletionCheck,
		map[string]any{"intent_id": intent.ID})
	var ready studiocontrol.Completion
	decodeStudioResult(t, readyResponse, &ready)
	if !ready.Ready {
		t.Fatalf("correlated completion = %+v", ready)
	}

	material := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationStudioIntentCompile,
		"compile-ambiguous", studiocontrol.CompileInput{ProjectID: created.ID,
			OutcomeContractID: contract.ID, WorkspaceRevision: created.WorkspaceRevision,
			Goal: "Add paid access", Rationale: "Billing changes authority and cost", Delta: &delta,
			Assumptions: []studiocontrol.Assumption{{ID: "billing", Statement: "Charge users", Material: true,
				DecisionNeed: "Choose whether billing is authorized"}}})
	var ambiguous studiocontrol.Intent
	decodeStudioResult(t, material, &ambiguous)
	decision := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: controlplane.OperationStudioProposalDecide,
		Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: "ambiguous-accept",
		Payload: studioJSON(t, map[string]any{"intent_id": ambiguous.ID, "proposal_id": ambiguous.Proposals[0].ID,
			"accept": true, "reason": "Proceed"})})
	if decision.Error == nil || decision.Error.Code != controlplane.ErrorConflict {
		t.Fatalf("material ambiguity decision = %+v", decision)
	}
	resolved := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationStudioProposalDecide,
		"ambiguous-resolved", map[string]any{"intent_id": ambiguous.ID,
			"proposal_id": ambiguous.Proposals[0].ID, "accept": true, "reason": "User chose subscription billing",
			"assumption_decisions": map[string]string{"billing": "Enable subscription billing only after explicit checkout"}})
	var resolvedProposal studiocontrol.Proposal
	decodeStudioResult(t, resolved, &resolvedProposal)
	if resolvedProposal.Status != studiocontrol.ProposalAccepted {
		t.Fatalf("resolved material decision = %+v", resolvedProposal)
	}

	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := os.ReadFile(filepath.Join(dataRoot, "sessions.db"))
	if err != nil || bytes.Contains(database, []byte(contract.Goal)) || bytes.Contains(database, []byte(intent.Goal)) {
		t.Fatalf("Studio state was not encrypted: %v", err)
	}
	runtime, err = OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	resumed := studioQuery(t, ctx, runtime, actor, controlplane.OperationStudioIntentGet,
		map[string]any{"intent_id": intent.ID})
	if !bytes.Contains(resumed.Result, []byte(intent.ID.String())) {
		t.Fatalf("restart intent = %s", resumed.Result)
	}
	if err := os.WriteFile(filepath.Join(created.Root, "spec", "tasks.md"), []byte("hand-edited drift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	driftResponse := studioQuery(t, ctx, runtime, actor, controlplane.OperationStudioDriftGet,
		map[string]any{"intent_id": intent.ID})
	var drift studiocontrol.Drift
	decodeStudioResult(t, driftResponse, &drift)
	if !drift.GeneratedViewChanged || !drift.CompletionBlocked {
		t.Fatalf("generated drift = %+v", drift)
	}

	brownfieldRoot := filepath.Join(workspace, "brownfield")
	if err := os.MkdirAll(brownfieldRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brownfieldRoot, "go.mod"), []byte("module example.test/brownfield\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	brownfield := dispatchStudioProject(t, ctx, runtime, actor, controlplane.OperationProjectAttach,
		"attach-brownfield", map[string]any{"name": "Brownfield", "directory": brownfieldRoot, "trust": "reviewed"})
	brownfieldContract := dispatchContract(t, ctx, runtime, actor, "brownfield-contract", "Change existing behavior")
	mapped := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationStudioIntentCompile,
		"compile-brownfield", studiocontrol.CompileInput{ProjectID: brownfield.ID,
			OutcomeContractID: brownfieldContract.ID, WorkspaceRevision: brownfield.WorkspaceRevision,
			Goal: brownfieldContract.Goal, MappedRequirements: []string{"req.12"}})
	var mappedIntent studiocontrol.Intent
	decodeStudioResult(t, mapped, &mappedIntent)
	if len(mappedIntent.Proposals) != 0 || len(mappedIntent.MappedRequirements) != 1 {
		t.Fatalf("brownfield requirement mapping = %+v", mappedIntent)
	}
}

func productionStudioDelta() studiocontrol.SpecDelta {
	return studiocontrol.SpecDelta{UserVisibleBehavior: []string{"The current status is visible"},
		NonGoals: []string{"No production deployment"}, Constraints: []string{"Keep the shared runtime"},
		Risks:              []string{"Authentication regression"},
		Criteria:           []studiocontrol.Criterion{{ID: "status.visible", Description: "Current status is visible"}},
		SecurityBoundaries: []string{"Authenticated actor"}, DataBoundaries: []string{"Project state"},
		Migration: []string{"No migration"}, Rollback: []string{"Remove the status route"},
		Verification: []string{"go test ./..."},
		Tasks:        []studiocontrol.PlannedTask{{ID: "status.route", Title: "Implement status route", Criteria: []string{"status.visible"}}}}
}

func dispatchContract(t *testing.T, ctx context.Context, runtime *Runtime, actor uuid.UUID,
	key, goal string) workcontrol.OutcomeContract {
	t.Helper()
	response := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: controlplane.OperationWorkContractPut,
		Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: key,
		Payload: studioJSON(t, workcontrol.ContractInput{Goal: goal, Deliverable: "verified change",
			DoneCriteria:         []workcontrol.Criterion{{ID: "status.visible", Description: "Current status is visible"}},
			VerificationRequired: []string{"go test ./..."}, NextAction: "review specification"})})
	var contract workcontrol.OutcomeContract
	decodeStudioResult(t, response, &contract)
	return contract
}

func dispatchStudioProject(t *testing.T, ctx context.Context, runtime *Runtime, actor uuid.UUID,
	operation controlplane.Operation, key string, payload any) projectcontrol.Project {
	t.Helper()
	response := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: operation,
		Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: key, Payload: studioJSON(t, payload)})
	var project projectcontrol.Project
	decodeStudioResult(t, response, &project)
	return project
}

func dispatchStudio(t *testing.T, ctx context.Context, runtime *Runtime, actor uuid.UUID,
	operation controlplane.Operation, key string, payload any) controlplane.Response {
	t.Helper()
	response := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: operation,
		Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: key, Payload: studioJSON(t, payload)})
	if response.Error != nil {
		t.Fatalf("%s = %+v", operation, response)
	}
	return response
}

func studioQuery(t *testing.T, ctx context.Context, runtime *Runtime, actor uuid.UUID,
	operation controlplane.Operation, payload any) controlplane.Response {
	t.Helper()
	response := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindQuery, Operation: operation,
		Scope: controlplane.Scope{ActorID: actor}, Payload: studioJSON(t, payload)})
	if response.Error != nil {
		t.Fatalf("%s = %+v", operation, response)
	}
	return response
}

func decodeStudioResult(t *testing.T, response controlplane.Response, target any) {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("control-plane response = %+v", response)
	}
	if err := json.Unmarshal(response.Result, target); err != nil {
		t.Fatal(err)
	}
}

func studioJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
