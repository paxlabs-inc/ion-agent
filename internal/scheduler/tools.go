package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
)

func (service *Service) RegisterTools(
	ctx context.Context,
	manager *tools.Manager,
) error {
	if manager == nil {
		return errors.New("scheduler: tool manager is required")
	}
	registrations := []tools.Registration{
		{
			Name:        "schedule_create",
			Description: "Create one encrypted actor-scoped future or recurring wake-up.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"required":["label","kind","wake_message","idempotency_key"],
				"properties":{
					"label":{"type":"string","minLength":1,"maxLength":256},
					"kind":{"type":"string","enum":["once","cron"]},
					"delay_seconds":{"type":"integer","minimum":1,"maximum":31536000},
					"fire_at":{"type":"string","format":"date-time"},
					"cron_expr":{"type":"string","minLength":1,"maxLength":256},
					"timezone":{"type":"string","minLength":1,"maxLength":128},
					"wake_message":{"type":"string","minLength":1,"maxLength":32768},
					"payload":{"type":"object"},
					"idempotency_key":{"type":"string","minLength":1,"maxLength":256},
					"max_failures":{"type":"integer","minimum":1,"maximum":20}
				},
				"additionalProperties":false
			}`),
			Classification: tools.ClassificationYellow,
			Check:          func(context.Context) error { return nil },
			Handler:        service.createTool,
		},
		{
			Name:        "schedule_list",
			Description: "List this actor's encrypted schedules without exposing another actor.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{"limit":{"type":"integer","minimum":1,"maximum":256}},
				"additionalProperties":false
			}`),
			Classification: tools.ClassificationGreen,
			Check:          func(context.Context) error { return nil },
			Handler:        service.listTool,
		},
		{
			Name:        "schedule_get",
			Description: "Inspect one encrypted schedule owned by this actor.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"required":["alarm_id"],
				"properties":{"alarm_id":{"type":"string","format":"uuid"}},
				"additionalProperties":false
			}`),
			Classification: tools.ClassificationGreen,
			Check:          func(context.Context) error { return nil },
			Handler:        service.getTool,
		},
		{
			Name:        "schedule_cancel",
			Description: "Cancel one active schedule owned by this actor.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"required":["alarm_id"],
				"properties":{"alarm_id":{"type":"string","format":"uuid"}},
				"additionalProperties":false
			}`),
			Classification: tools.ClassificationYellow,
			Check:          func(context.Context) error { return nil },
			Handler:        service.cancelTool,
		},
	}
	for _, registration := range registrations {
		if err := manager.Register(ctx, registration); err != nil {
			return fmt.Errorf("scheduler: register %s: %w", registration.Name, err)
		}
	}
	return nil
}

func (service *Service) createTool(
	ctx context.Context,
	raw json.RawMessage,
) (json.RawMessage, error) {
	scope, err := scheduleScope(ctx)
	if err != nil {
		return nil, err
	}
	var input struct {
		Label          string          `json:"label"`
		Kind           Kind            `json:"kind"`
		DelaySeconds   int64           `json:"delay_seconds"`
		FireAt         string          `json:"fire_at"`
		CronExpr       string          `json:"cron_expr"`
		Timezone       string          `json:"timezone"`
		WakeMessage    string          `json:"wake_message"`
		Payload        json.RawMessage `json:"payload"`
		IdempotencyKey string          `json:"idempotency_key"`
		MaxFailures    int             `json:"max_failures"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	alarm, deduplicated, err := service.Create(ctx, CreateRequest{
		ActorID: scope.ActorID, SessionID: *scope.SessionID,
		Label: input.Label, Kind: input.Kind,
		DelaySeconds: input.DelaySeconds, FireAt: input.FireAt,
		CronExpr: input.CronExpr, Timezone: input.Timezone,
		WakeMessage: input.WakeMessage, Payload: input.Payload,
		IdempotencyKey: input.IdempotencyKey, MaxFailures: input.MaxFailures,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"alarm": projectionOf(alarm), "deduplicated": deduplicated,
	})
}

func (service *Service) listTool(
	ctx context.Context,
	raw json.RawMessage,
) (json.RawMessage, error) {
	scope, err := scheduleScope(ctx)
	if err != nil {
		return nil, err
	}
	var input struct {
		Limit int `json:"limit"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	alarms := service.List(scope.ActorID, input.Limit)
	views := make([]Projection, 0, len(alarms))
	for _, alarm := range alarms {
		views = append(views, projectionOf(alarm))
	}
	return json.Marshal(map[string]any{"alarms": views, "count": len(views)})
}

func (service *Service) getTool(
	ctx context.Context,
	raw json.RawMessage,
) (json.RawMessage, error) {
	scope, err := scheduleScope(ctx)
	if err != nil {
		return nil, err
	}
	alarmID, err := decodeAlarmID(raw)
	if err != nil {
		return nil, err
	}
	alarm, err := service.Get(scope.ActorID, alarmID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(alarm)
}

func (service *Service) cancelTool(
	ctx context.Context,
	raw json.RawMessage,
) (json.RawMessage, error) {
	scope, err := scheduleScope(ctx)
	if err != nil {
		return nil, err
	}
	alarmID, err := decodeAlarmID(raw)
	if err != nil {
		return nil, err
	}
	alarm, err := service.Cancel(ctx, scope.ActorID, alarmID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(projectionOf(alarm))
}

func scheduleScope(ctx context.Context) (controlplane.ApprovalScope, error) {
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok || scope.SessionID == nil || *scope.SessionID == uuid.Nil {
		return controlplane.ApprovalScope{}, errors.New(
			"scheduler: authenticated actor and session scope are required",
		)
	}
	return scope, nil
}

func decodeAlarmID(raw json.RawMessage) (uuid.UUID, error) {
	var input struct {
		AlarmID string `json:"alarm_id"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.Parse(strings.TrimSpace(input.AlarmID))
	if err != nil {
		return uuid.Nil, errors.New("scheduler: valid alarm_id is required")
	}
	return id, nil
}

func decodeStrict(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("scheduler: invalid arguments: %w", err)
	}
	return nil
}

func projectionOf(alarm Alarm) Projection {
	projection := Projection{
		ID: alarm.ID, Label: alarm.Label, Kind: alarm.Kind,
		Status: alarm.Status, Timezone: alarm.Timezone,
		CronExpr: alarm.CronExpr, LastFiredAt: cloneTime(alarm.LastFiredAt),
		FailureCount:     alarm.FailureCount,
		LastFailureCount: alarm.LastFailureCount,
		LastError:        alarm.LastError, Source: "agent_schedule",
	}
	if alarm.Status == StatusActive || alarm.Status == StatusClaimed {
		next := alarm.NextFireAt
		projection.NextFireAt = &next
	}
	return projection
}
