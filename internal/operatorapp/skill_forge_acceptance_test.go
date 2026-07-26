package operatorapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/skills"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

func TestProductionSkillForgeStagesGatesAndPromotesEvidenceBackedRevision(
	t *testing.T,
) {
	ctx := context.Background()
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.capabilityRoot.SkillCommand(
		ctx, controlplane.OperationSkillSave, json.RawMessage(`{
			"name":"Browser account workflow",
			"trigger":"create an account",
			"steps":["Open the canonical origin"],
			"pitfalls":["Do not expose verification secrets"],
			"verification":["Confirm the account result"]
		}`),
	); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.capabilityRoot.SkillCommand(
		ctx, controlplane.OperationSkillRefine, json.RawMessage(`{
			"name":"Browser account workflow",
			"steps":["Use the dedicated machine-mail address"],
			"verification":["Confirm exactly-once submit"],
			"evidence":[{
				"episode_id":"browser-replay-17",
				"outcome":"verified_success",
				"summary":"Private mailbox handoff completed signup without exposing the code.",
				"verifier":"browser completion receipt"
			}]
		}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	candidate, ok := result.(skills.Candidate)
	if !ok || candidate.Status != skills.CandidatePending {
		t.Fatalf("staged candidate = %#v", result)
	}
	active, err := runtime.capabilityRoot.skills.Load(ctx, candidate.SkillName)
	if err != nil || active.Revision != 1 || len(active.Steps) != 1 {
		t.Fatalf("candidate changed active skill: %+v, %v", active, err)
	}
	arguments, _ := json.Marshal(map[string]any{
		"name": candidate.SkillName, "candidate_id": candidate.ID,
		"baseline_score": 0.5, "candidate_score": 0.9,
		"validation_cases": 3, "safety_passed": true,
		"validation_run_ids": []string{"heldout-1", "heldout-2", "heldout-3"},
	})
	call := protocol.NormalizedToolCall{
		ID: "skill-gate", Name: "skill_candidate_evaluate", Arguments: arguments,
	}
	actorID, sessionID := uuid.New(), uuid.New()
	base := controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
		ActorID: actorID, SessionID: &sessionID,
	})
	unapproved := policy.WithPrincipal(base, policy.Principal{Sender: policy.SenderUser})
	if _, err := runtime.capabilityRoot.manager.Execute(unapproved, call); !errors.Is(err, tools.ErrPolicyDenied) {
		t.Fatalf("unapproved promotion error = %v", err)
	}
	approved := policy.WithPrincipal(base, policy.Principal{
		Sender: policy.SenderUser, Approved: true,
	})
	decisionPayload, err := runtime.capabilityRoot.manager.Execute(approved, call)
	if err != nil {
		t.Fatal(err)
	}
	var decision skills.Candidate
	if err := json.Unmarshal(decisionPayload, &decision); err != nil {
		t.Fatal(err)
	}
	if decision.Status != skills.CandidateAdopted {
		t.Fatalf("approved decision = %+v", decision)
	}
	active, err = runtime.capabilityRoot.skills.Load(ctx, candidate.SkillName)
	if err != nil || active.Revision != 2 || len(active.Steps) != 2 {
		t.Fatalf("promoted active skill = %+v, %v", active, err)
	}
	lifecycleResponse := runtime.dispatcher.Dispatch(
		ctx,
		actorID,
		controlplane.Request{
			ProtocolVersion: controlplane.ProtocolVersion,
			RequestID:       uuid.New(),
			Kind:            controlplane.KindQuery,
			Operation:       controlplane.OperationSkillLifecycle,
			Scope:           controlplane.Scope{ActorID: actorID},
		},
	)
	if lifecycleResponse.Error != nil {
		t.Fatalf("skill lifecycle query = %+v", lifecycleResponse)
	}
	if bytes.Contains(lifecycleResponse.Result, []byte("browser-replay-17")) ||
		bytes.Contains(lifecycleResponse.Result, []byte("Private mailbox handoff")) ||
		len(lifecycleResponse.Result) > 32_000 {
		t.Fatalf("skill lifecycle projection is unbounded or leaks evidence: %s", lifecycleResponse.Result)
	}
	var lifecycle skills.Lifecycle
	if err := json.Unmarshal(lifecycleResponse.Result, &lifecycle); err != nil {
		t.Fatal(err)
	}
	if len(lifecycle.Active) != 1 ||
		len(lifecycle.Candidates) != 1 ||
		lifecycle.Candidates[0].Status != skills.CandidateAdopted ||
		len(lifecycle.Retired) != 1 {
		t.Fatalf("skill lifecycle projection = %+v", lifecycle)
	}
	rollbackPayload, _ := json.Marshal(map[string]any{
		"name": active.Name, "revision": 1,
	})
	rollbackResponse := runtime.dispatcher.Dispatch(
		ctx,
		actorID,
		controlplane.Request{
			ProtocolVersion: controlplane.ProtocolVersion,
			RequestID:       uuid.New(),
			Kind:            controlplane.KindCommand,
			Operation:       controlplane.OperationSkillRollback,
			Scope:           controlplane.Scope{ActorID: actorID},
			IdempotencyKey:  "rollback-skill-revision-1",
			Payload:         rollbackPayload,
		},
	)
	if rollbackResponse.Error != nil {
		t.Fatalf("skill rollback = %+v", rollbackResponse)
	}
	var rolledBack skills.Skill
	if err := json.Unmarshal(rollbackResponse.Result, &rolledBack); err != nil {
		t.Fatal(err)
	}
	if rolledBack.Revision != 3 || len(rolledBack.Steps) != 1 {
		t.Fatalf("rolled back production skill = %+v", rolledBack)
	}
}
