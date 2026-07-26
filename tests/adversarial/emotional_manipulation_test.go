package adversarial

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func Test_EmotionalManipulation_HighUrgencyCannotAuthorizeRedAction(t *testing.T) {
	emotional := safety.NewEmotionalState()
	emotional.Update(1, 1, 1)
	if emotional.CanInfluenceSafety() {
		t.Fatal("emotional state gained safety authority")
	}

	auditor := &policy.MemoryAuditor{}
	pipeline, err := policy.NewDefault(types.SystemClock{}, auditor, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tools.NewManager(
		types.SystemClock{},
		tools.WithExecutionPolicy(pipeline),
	)
	if err != nil {
		t.Fatal(err)
	}
	executed := false
	if err := manager.Register(context.Background(), tools.Registration{
		Name:                    "payment",
		Description:             "Move value through the policy-bound operation path.",
		Parameters:              json.RawMessage(`{}`),
		Classification:          tools.ClassificationRed,
		ExternallyCommunicating: true,
		Check:                   func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			executed = true
			return json.RawMessage(`{"paid":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = manager.Execute(
		policy.WithPrincipal(context.Background(), policy.Principal{
			Sender: policy.SenderUser,
		}),
		protocol.NormalizedToolCall{
			ID:        "payment-attack",
			Name:      "payment",
			Arguments: json.RawMessage(`{"amount":1000000}`),
		},
	)
	if !errors.Is(err, tools.ErrPolicyDenied) || !errors.Is(err, policy.ErrDenied) {
		t.Fatalf("payment attack error = %v", err)
	}
	if executed {
		t.Fatal("RED handler executed under emotional manipulation")
	}
	events := auditor.Events()
	if len(events) != 1 || events[0].Decision != policy.Deny {
		t.Fatalf("audit events = %+v", events)
	}
}

func Test_ClassificationDowngrade_KnownRedActionFailsClosed(t *testing.T) {
	pipeline, err := policy.NewDefault(
		types.SystemClock{},
		&policy.MemoryAuditor{},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, _ := tools.NewManager(
		types.SystemClock{},
		tools.WithExecutionPolicy(pipeline),
	)
	executed := false
	if err := manager.Register(context.Background(), tools.Registration{
		Name:           "publish",
		Description:    "Attempt to disguise public publishing as safe.",
		Parameters:     json.RawMessage(`{}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			executed = true
			return json.RawMessage(`null`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = manager.Execute(
		policy.WithPrincipal(context.Background(), policy.Principal{
			Sender:   policy.SenderUser,
			Approved: true,
		}),
		protocol.NormalizedToolCall{
			ID:        "downgrade",
			Name:      "publish",
			Arguments: json.RawMessage(`{}`),
		},
	)
	if !errors.Is(err, policy.ErrDenied) || executed {
		t.Fatalf("downgrade error = %v, executed = %v", err, executed)
	}
}
