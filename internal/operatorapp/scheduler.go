package operatorapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	agentscheduler "github.com/paxlabs-inc/ion-agent/internal/scheduler"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
)

type scheduledTurnWaker struct {
	dispatcher *controlplane.Dispatcher
}

func (waker scheduledTurnWaker) Wake(
	ctx context.Context,
	wake agentscheduler.Wake,
) error {
	if waker.dispatcher == nil || wake.ActorID == uuid.Nil ||
		wake.SessionID == uuid.Nil || wake.AlarmID == uuid.Nil ||
		wake.OccurrenceID == "" {
		return fmt.Errorf("operator scheduler: complete wake scope is required")
	}
	content := wake.Message
	if payload := bytes.TrimSpace(wake.Payload); len(payload) > 0 &&
		!bytes.Equal(payload, []byte(`{}`)) {
		encoded, err := json.Marshal(struct {
			Origin       string          `json:"origin"`
			AlarmID      uuid.UUID       `json:"alarm_id"`
			OccurrenceID string          `json:"occurrence_id"`
			Payload      json.RawMessage `json:"payload"`
		}{
			Origin: "agent_schedule", AlarmID: wake.AlarmID,
			OccurrenceID: wake.OccurrenceID,
			Payload:      append(json.RawMessage(nil), wake.Payload...),
		})
		if err != nil {
			return fmt.Errorf("operator scheduler: encode wake context: %w", err)
		}
		content += "\n\nScheduled context (stored by this agent):\n" + string(encoded)
	}
	request := controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindCommand,
		Operation:       controlplane.OperationTurnSubmit,
		Scope: controlplane.Scope{
			ActorID: wake.ActorID, SessionID: &wake.SessionID,
			Profile: "scheduled", Channel: "scheduler",
		},
		IdempotencyKey: "schedule:" + wake.OccurrenceID,
		Payload: schedulerJSON(map[string]any{
			"content": content, "surface": "general",
		}),
	}
	dispatchContext := policy.WithPrincipal(ctx, policy.Principal{
		Sender: policy.SenderScheduler, Profile: "scheduled",
	})
	response := waker.dispatcher.Dispatch(dispatchContext, wake.ActorID, request)
	if response.Error != nil {
		return fmt.Errorf(
			"operator scheduler: wake turn %s: %s",
			response.Error.Code, response.Error.Message,
		)
	}
	return nil
}

func schedulerJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
