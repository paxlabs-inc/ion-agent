package operatorapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
)

func TestDisciplinedWorkProductionBoundaryRestartIsolationAndFalseCompletion(t *testing.T) {
	ctx := context.Background()
	directory, workspace := t.TempDir(), t.TempDir()
	initializeRuntimeVault(t, ctx, directory)
	config := RuntimeConfig{DataDirectory: directory, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace,
		Clock:              &acceptanceClock{now: time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)}}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	actor, other := uuid.New(), uuid.New()
	sessionID := createAcceptanceSession(t, ctx, runtime, actor, "disciplined-work")
	scope := controlplane.Scope{ActorID: actor, SessionID: &sessionID}
	contractPayload, _ := json.Marshal(workcontrol.ContractInput{
		Goal: "Produce a restart-safe evidence report", Deliverable: "deliverable.txt",
		DoneCriteria:         []workcontrol.Criterion{{ID: "content", Description: "report contains current acceptance evidence"}},
		VerificationRequired: []string{"server digest"}, NextAction: "write and verify the report",
	})
	created := dispatchWorkCommand(t, ctx, runtime, scope, controlplane.OperationWorkContractPut, "contract-one", contractPayload)
	var contract workcontrol.OutcomeContract
	if err := json.Unmarshal(created.Result, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.ID == uuid.Nil || contract.SessionID == nil || *contract.SessionID != sessionID {
		t.Fatalf("contract = %+v", contract)
	}
	completePayload, _ := json.Marshal(map[string]any{"contract_id": contract.ID})
	denied := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion, RequestID: uuid.New(), Kind: controlplane.KindCommand,
		Operation: controlplane.OperationWorkComplete, Scope: scope, IdempotencyKey: "complete-too-early", Payload: completePayload,
	})
	if denied.Error == nil {
		t.Fatal("contract completed without verified evidence")
	}
	if err := os.WriteFile(filepath.Join(workspace, "deliverable.txt"), []byte("acceptance passed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recordPayload, _ := json.Marshal(workcontrol.ArtifactInput{ContractID: contract.ID,
		Kind: "report", Title: "Acceptance evidence", Reference: "deliverable.txt", CriteriaCovered: []string{"content"}})
	recorded := dispatchWorkCommand(t, ctx, runtime, scope, controlplane.OperationArtifactRecord, "artifact-one", recordPayload)
	var artifact workcontrol.Artifact
	if err := json.Unmarshal(recorded.Result, &artifact); err != nil {
		t.Fatal(err)
	}
	verifyPayload, _ := json.Marshal(map[string]any{"artifact_id": artifact.ID})
	verified := dispatchWorkCommand(t, ctx, runtime, scope, controlplane.OperationArtifactVerify, "verify-one", verifyPayload)
	if err := json.Unmarshal(verified.Result, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.VerifiedAt == nil || len(artifact.SHA256) != 64 {
		t.Fatalf("verified artifact = %+v", artifact)
	}
	dispatchWorkCommand(t, ctx, runtime, scope, controlplane.OperationWorkComplete, "complete-one", completePayload)
	otherBrief, err := runtime.capabilityRoot.work.Brief(ctx, other, nil)
	if err != nil {
		t.Fatal(err)
	}
	if otherBrief.Contract != nil || len(otherBrief.Deliverables) != 0 {
		t.Fatalf("cross-actor brief leaked: %+v", otherBrief)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	portfolio, err := restarted.capabilityRoot.work.Get(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(portfolio.Contracts) != 1 || portfolio.Contracts[0].Status != workcontrol.StatusCompleted ||
		len(portfolio.Artifacts) != 1 || portfolio.Artifacts[0].VerifiedAt == nil {
		t.Fatalf("restart portfolio = %+v", portfolio)
	}
}

func dispatchWorkCommand(t *testing.T, ctx context.Context, runtime *Runtime, scope controlplane.Scope,
	operation controlplane.Operation, key string, payload json.RawMessage) controlplane.Response {
	t.Helper()
	response := runtime.dispatcher.Dispatch(ctx, scope.ActorID, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion, RequestID: uuid.New(), Kind: controlplane.KindCommand,
		Operation: operation, Scope: scope, IdempotencyKey: key, Payload: payload,
	})
	if response.Error != nil {
		t.Fatalf("%s response = %+v", operation, response)
	}
	return response
}
