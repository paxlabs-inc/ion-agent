package operatorapp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/skills"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type skillProposalGenerator struct {
	calls   int
	content string
}

func (generator *skillProposalGenerator) Generate(
	context.Context,
	protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	generator.calls++
	if generator.content != "" {
		return protocol.NormalizedGeneration{
			Content: generator.content, FinishReason: protocol.FinishStop,
		}, nil
	}
	return protocol.NormalizedGeneration{
		Content: `{
			"name":"Reconcile a durable document export",
			"description":"Resume a durable document export without repeating a completed delivery.",
			"trigger":"reconcile durable document export",
			"steps":[
				"Inspect the authoritative artifact receipt",
				"Resume only the unfinished delivery boundary"
			],
			"pitfalls":["Never repeat a receipted external delivery"],
			"verification":["Verify the artifact and delivery receipts"]
		}`,
		FinishReason: protocol.FinishStop,
	}, nil
}

func TestVerifiedNovelTurnAutomaticallyAuthorsGatesAndActivatesSkill(
	t *testing.T,
) {
	ctx := context.Background()
	turnID, sessionID := uuid.New(), uuid.New()
	ctx = controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
		ActorID: uuid.New(), SessionID: &sessionID, TurnID: &turnID,
	})
	store, err := skills.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	generator := &skillProposalGenerator{}
	author := skillAutoAuthor{
		generator: generator, store: store, model: "acceptance",
	}
	task := "Reconcile a durable document export after an interrupted delivery, " +
		"inspect the authoritative artifact receipt, avoid duplicate external " +
		"effects, and verify that exactly one delivery receipt remains after recovery."
	response := agent.Response{
		Content: "The durable document export resumed from its artifact receipt " +
			"and exactly one verified delivery receipt remains.",
		ToolEvents: []agent.ToolExecution{
			{
				Call: protocol.NormalizedToolCall{
					ID: "artifact", Name: "artifact_verify",
					Arguments: json.RawMessage(`{"id":"artifact"}`),
				},
				Result: json.RawMessage(`{"verified":true}`),
			},
			{
				Call: protocol.NormalizedToolCall{
					ID: "delivery", Name: "delivery_reconcile",
					Arguments: json.RawMessage(`{"id":"delivery"}`),
				},
				Result: json.RawMessage(`{"receipts":1}`),
			},
		},
	}
	decision, attempted, err := author.Learn(
		ctx, task, response, nil,
		skills.MatchContext{
			Platform: "linux",
			Tools: map[string]struct{}{
				"artifact_verify":    {},
				"delivery_reconcile": {},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !attempted || generator.calls != 1 ||
		decision.Status != skills.CandidateAdopted ||
		decision.Evaluation == nil ||
		decision.Evaluation.ValidationCases != 3 {
		t.Fatalf(
			"automatic skill decision = %+v attempted=%t calls=%d",
			decision, attempted, generator.calls,
		)
	}
	active, err := store.Load(ctx, decision.SkillName)
	if err != nil || active.Revision != 1 || active.Origin != "authored" {
		t.Fatalf("active learned skill = %+v, %v", active, err)
	}
	recurrence, err := store.MatchAll(
		ctx, "Please reconcile durable document export for another artifact",
		skills.MatchContext{
			Platform: "linux",
			Tools: map[string]struct{}{
				"artifact_verify":    {},
				"delivery_reconcile": {},
			},
		},
		3,
	)
	if err != nil || len(recurrence) != 1 ||
		recurrence[0].Name != active.Name {
		t.Fatalf("learned recurrence = %+v, %v", recurrence, err)
	}
}

func TestSkillLearningRejectsFailedUnverifiedAndSensitiveTurns(t *testing.T) {
	store, err := skills.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	generator := &skillProposalGenerator{}
	author := skillAutoAuthor{
		generator: generator, store: store, model: "acceptance",
	}
	longTask := "Complete a novel multi-step recovery procedure with enough " +
		"specific bounded work to qualify for procedural learning after the " +
		"authoritative execution evidence has been checked and reconciled."
	for _, test := range []struct {
		name     string
		task     string
		response agent.Response
	}{
		{
			name: "failed",
			task: longTask,
			response: agent.Response{
				Content: "The work failed.",
				ToolEvents: []agent.ToolExecution{{
					Call: protocol.NormalizedToolCall{
						ID: "failed", Name: "shell_execute",
						Arguments: json.RawMessage(`{}`),
					},
					Error: "exit status 1",
				}},
			},
		},
		{
			name: "unverified",
			task: longTask,
			response: agent.Response{
				Content: "The work is probably complete.",
			},
		},
		{
			name: "sensitive",
			task: longTask + " Use this API key while doing it.",
			response: agent.Response{
				Content: "The work completed.",
				ToolEvents: []agent.ToolExecution{
					successfulLearningTool("one"),
					successfulLearningTool("two"),
				},
			},
		},
		{
			name: "acceptance-only",
			task: longTask,
			response: agent.Response{
				Content: "The work was accepted for later processing.",
				ToolEvents: []agent.ToolExecution{
					{
						Call: protocol.NormalizedToolCall{
							ID: "one", Name: "artifact_verify",
							Arguments: json.RawMessage(`{}`),
						},
						Result: json.RawMessage(`{"accepted":true}`),
					},
					successfulLearningTool("two"),
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, attempted, learnErr := author.Learn(
				context.Background(), test.task, test.response, nil,
				skills.MatchContext{Platform: "linux"},
			)
			if learnErr != nil || attempted {
				t.Fatalf("learning attempted=%t error=%v", attempted, learnErr)
			}
		})
	}
	if generator.calls != 0 {
		t.Fatalf("rejected turns invoked skill author %d times", generator.calls)
	}
	if installed, err := store.List(context.Background()); err != nil ||
		len(installed) != 0 {
		t.Fatalf("rejected turns installed skills: %+v, %v", installed, err)
	}
}

func TestSkillLearningRejectsGeneratedOneOffIdentifiers(t *testing.T) {
	store, err := skills.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	generator := &skillProposalGenerator{content: `{
		"name":"Recover export 12345678",
		"description":"Recover the artifact stored at /tmp/private/export.pdf.",
		"trigger":"recover export 12345678",
		"steps":["Inspect the receipt","Resume the export"],
		"pitfalls":["Avoid duplicate delivery"],
		"verification":["Verify the delivery receipt"]
	}`}
	author := skillAutoAuthor{
		generator: generator, store: store, model: "acceptance",
	}
	task := "Complete a novel durable export recovery with authoritative artifact " +
		"and delivery evidence, without repeating the external effect, and verify " +
		"the final artifact before reporting that the recovery is complete."
	_, attempted, err := author.Learn(
		context.Background(),
		task,
		agent.Response{
			Content: "The durable export was recovered and verified.",
			ToolEvents: []agent.ToolExecution{
				successfulLearningTool("one"),
				successfulLearningTool("two"),
			},
		},
		nil,
		skills.MatchContext{
			Platform: "linux",
			Tools:    map[string]struct{}{"artifact_verify": {}},
		},
	)
	if err == nil || !attempted || generator.calls != 1 {
		t.Fatalf("one-off learning attempted=%t calls=%d error=%v", attempted, generator.calls, err)
	}
	if installed, listErr := store.List(context.Background()); listErr != nil ||
		len(installed) != 0 {
		t.Fatalf("one-off proposal activated: %+v, %v", installed, listErr)
	}
}

func successfulLearningTool(id string) agent.ToolExecution {
	return agent.ToolExecution{
		Call: protocol.NormalizedToolCall{
			ID: id, Name: "artifact_verify", Arguments: json.RawMessage(`{}`),
		},
		Result: json.RawMessage(`{"verified":true}`),
	}
}

var _ agent.Generator = (*skillProposalGenerator)(nil)
