package operatorapp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type operatorSupervisorExecutor func(
	context.Context, workcontrol.TaskPacket, uuid.UUID,
) (workcontrol.WorkerResult, error)

func (executor operatorSupervisorExecutor) Execute(
	ctx context.Context, packet workcontrol.TaskPacket, attemptID uuid.UUID,
) (workcontrol.WorkerResult, error) {
	return executor(ctx, packet, attemptID)
}

func TestAgentSupervisorProductionSurfaceShowsTwentyLiveSpecialists(t *testing.T) {
	ctx := context.Background()
	directory, workspace := t.TempDir(), t.TempDir()
	initializeRuntimeVault(t, ctx, directory)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: directory, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace,
		Clock:              &acceptanceClock{now: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	actor := uuid.New()
	sessionID := createAcceptanceSession(t, ctx, runtime, actor, "wide-work")
	scope := controlplane.Scope{ActorID: actor, SessionID: &sessionID}
	criteria := make([]workcontrol.Criterion, workcontrol.LaunchParallelMinimum)
	items := make([]workcontrol.WorkItemInput, workcontrol.LaunchParallelMinimum)
	for index := range criteria {
		id := fmt.Sprintf("stream-%02d", index)
		criteria[index] = workcontrol.Criterion{ID: id, Description: "Inspect " + id}
		items[index] = workcontrol.WorkItemInput{
			ID: id, Title: "Inspect " + id, Criteria: []string{id},
		}
	}
	contract, _, err := runtime.capabilityRoot.work.PutContractWithWorkItems(
		ctx, actor,
		workcontrol.ContractInput{
			SessionID: &sessionID, Goal: "Run wide work",
			Deliverable: "twenty specialist findings", DoneCriteria: criteria,
			VerificationRequired: []string{"current evidence"},
			NextAction:           "dispatch independent workstreams",
		},
		items,
	)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var active atomic.Int32
	runtime.capabilityRoot.supervisor.SetExecutor(operatorSupervisorExecutor(func(
		ctx context.Context, packet workcontrol.TaskPacket, attemptID uuid.UUID,
	) (workcontrol.WorkerResult, error) {
		active.Add(1)
		defer active.Add(-1)
		select {
		case <-ctx.Done():
			return workcontrol.WorkerResult{}, ctx.Err()
		case <-release:
		}
		return workcontrol.WorkerResult{
			AttemptID: attemptID, WorkerID: "acceptance-" + packet.ID,
			Status: workcontrol.SpecialistCompleted, Progress: 100,
			Summary: "completed " + packet.ID,
		}, nil
	}))
	startPayload, _ := json.Marshal(map[string]any{"contract_id": contract.ID})
	started := dispatchWorkCommand(
		t, ctx, runtime, scope, controlplane.OperationSupervisorStart,
		"start-wide-work", startPayload,
	)
	var run workcontrol.SupervisorRun
	if err := json.Unmarshal(started.Result, &run); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for active.Load() != workcontrol.LaunchParallelMinimum && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if active.Load() != workcontrol.LaunchParallelMinimum {
		t.Fatalf("live specialists = %d", active.Load())
	}
	listed := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion, RequestID: uuid.New(),
		Kind: controlplane.KindQuery, Operation: controlplane.OperationSupervisorList,
		Scope: scope, Payload: json.RawMessage(`{}`),
	})
	if listed.Error != nil {
		t.Fatalf("supervisor list = %+v", listed)
	}
	var runs []workcontrol.SupervisorRun
	if err := json.Unmarshal(listed.Result, &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || len(runs[0].Tasks) != workcontrol.LaunchParallelMinimum ||
		runs[0].Budget.MaxParallel != workcontrol.LaunchParallelMinimum {
		t.Fatalf("supervisor projection = %+v", runs)
	}
	for _, task := range runs[0].Tasks {
		if task.Status != workcontrol.SpecialistRunning ||
			task.Progress != 5 || len(task.Attempts) != 1 {
			t.Fatalf("specialist progress = %+v", task)
		}
	}
	cancelPayload, _ := json.Marshal(map[string]any{"run_id": run.ID})
	cancelled := dispatchWorkCommand(
		t, ctx, runtime, scope, controlplane.OperationSupervisorCancel,
		"cancel-wide-work", cancelPayload,
	)
	run = workcontrol.SupervisorRun{}
	if err := json.Unmarshal(cancelled.Result, &run); err != nil {
		t.Fatal(err)
	}
	if run.Status != workcontrol.SupervisorCancelled || len(run.Leases) != 0 {
		t.Fatalf("cancelled supervisor = %+v", run)
	}
	close(release)
}

func TestSupervisorStartSchemaAndExecutorEvidenceAccounting(t *testing.T) {
	t.Parallel()
	if err := tools.ValidateArguments(
		json.RawMessage(supervisorStartToolSchema),
		json.RawMessage(`{
			"contract_id":"11111111-1111-4111-8111-111111111111",
			"project_id":"22222222-2222-4222-8222-222222222222",
			"project_budget":{
				"max_parallel":20,
				"max_tokens":400000,
				"max_cost_cents":5000,
				"max_tool_calls":640,
				"max_wall_seconds":7200,
				"max_processes":32,
				"max_storage_bytes":2147483648,
				"max_network_bytes":1073741824,
				"max_provider_cents":5000,
				"max_retries":2
			},
			"overrides":[{
				"work_item_id":"frontend",
				"specialist":"frontend",
				"scope":{"read_files":["workspace"],"write_files":["ui/web"]},
				"tools":["filesystem_read","filesystem_write"],
				"budget":{"max_tokens":12000,"max_tool_calls":8}
			}]
		}`),
	); err != nil {
		t.Fatalf("supervisor_start schema rejected accepted fields: %v", err)
	}
	contractID := uuid.New()
	firstID, secondID := uuid.New(), uuid.New()
	verifiedAt := time.Now().UTC()
	artifactResult := func(
		id uuid.UUID,
		criteria []string,
	) json.RawMessage {
		raw, err := json.Marshal(map[string]any{
			"id": id, "contract_id": contractID,
			"criteria_covered": criteria,
			"sha256":           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"verification":     "server_sha256",
			"verified_at":      verifiedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	response := agent.Response{
		Usage: protocol.TokenUsage{
			TotalTokens:             1_200,
			ModelCostMicrocents:     1_400_000,
			ProviderSpendMicrocents: 1_750_000,
			ModelCostKnown:          true, ProviderSpendKnown: true,
		},
		ToolEvents: []agent.ToolExecution{
			{
				Call: protocol.NormalizedToolCall{
					Name: "artifact_verify",
				},
				Result: artifactResult(firstID, []string{"a"}),
			},
			{
				Call: protocol.NormalizedToolCall{
					Name: "artifact_verify",
				},
				Result: artifactResult(secondID, []string{"b"}),
			},
		},
	}
	packet := workcontrol.TaskPacket{
		ContractID: contractID,
		Criteria:   []string{"a", "b"},
	}
	artifacts := verifiedSupervisorArtifacts(response, packet)
	usage := specialistExecutionUsage(response, time.Now())
	if len(artifacts) != 2 ||
		usage.Tokens != 1_200 ||
		usage.CostCents != 2 ||
		usage.ProviderCents != 2 ||
		!usage.CostKnown ||
		!usage.ProviderSpendKnown {
		t.Fatalf("artifacts=%v usage=%+v", artifacts, usage)
	}
}

var _ workcontrol.SupervisorExecutor = operatorSupervisorExecutor(nil)
