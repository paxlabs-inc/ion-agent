package operatorapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/swarm"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func TestSurfaceServiceRejectsUnavailableConfigMutationWithoutRetainingSecrets(t *testing.T) {
	t.Parallel()
	service, err := NewSurfaceService(RuntimeInfo{StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	actorID := uuid.New()
	first := controlplane.Scope{ActorID: actorID, Profile: "first", Channel: "local"}
	command := controlplane.Request{
		Kind: controlplane.KindCommand, Operation: controlplane.OperationConfigPatch,
		Scope: first, Payload: json.RawMessage(`{"secrets":{"PROVIDER_API_KEY":"never-return"}}`),
	}
	if err := service.AuthorizeMutation(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	result, err := service.Command(context.Background(), command)
	var public controlplane.PublicError
	if result != nil || !errors.As(err, &public) ||
		public.Code != controlplane.ErrorUnavailable {
		t.Fatalf("config patch result = %s, error = %v", result, err)
	}
	payload, err := service.Query(context.Background(), controlplane.Request{
		Kind: controlplane.KindQuery, Operation: controlplane.OperationConfigGet,
		Scope: first, Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "never-return") ||
		strings.Contains(string(payload), "PROVIDER_API_KEY") ||
		!strings.Contains(string(payload), `"mutation_status":"unavailable"`) {
		t.Fatalf("configuration projection = %s", payload)
	}
}

func TestSurfaceServiceReportsRealProviderUsageWithoutCredentialMetadata(t *testing.T) {
	t.Parallel()
	service, err := NewSurfaceService(RuntimeInfo{
		StartedAt: time.Now(),
		ProviderUsage: func() map[string]any {
			return map[string]any{
				"requests": 7, "failures": 1, "total_tokens": 42,
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Query(context.Background(), controlplane.Request{
		Kind: controlplane.KindQuery, Operation: controlplane.OperationProviderUsage,
		Scope: controlplane.Scope{ActorID: uuid.New()}, Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"failures":1,"requests":7,"total_tokens":42}` ||
		strings.Contains(string(result), "credential") {
		t.Fatalf("provider usage = %s", result)
	}
}

func TestSurfaceServiceUnsupportedReadProjectionsAreExplicitlyUnavailable(t *testing.T) {
	t.Parallel()
	service, err := NewSurfaceService(RuntimeInfo{StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []controlplane.Operation{
		controlplane.OperationMemoryGraph,
		controlplane.OperationMemoryActivation,
		controlplane.OperationCitationVerify,
		controlplane.OperationDreamweaverBeliefs,
		controlplane.OperationMCPServers,
		controlplane.OperationPolicyExplain,
		controlplane.OperationReceiptList,
		controlplane.OperationReceiptVerify,
		controlplane.OperationIntegrityVerify,
		controlplane.OperationLogsQuery,
	} {
		result, queryErr := service.Query(context.Background(), controlplane.Request{
			Kind: controlplane.KindQuery, Operation: operation,
			Scope:   controlplane.Scope{ActorID: uuid.New()},
			Payload: json.RawMessage(`{}`),
		})
		if queryErr != nil {
			t.Fatalf("%s: %v", operation, queryErr)
		}
		var projection struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(result, &projection); err != nil ||
			projection.Status != "unavailable" ||
			strings.TrimSpace(projection.Reason) == "" {
			t.Fatalf("%s projection = %s, %v", operation, result, err)
		}
	}
}

func TestSurfaceMutationsUseToolPolicyAndAtomicScopedSwarmAbort(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := types.SystemClock{}
	auditor := &policy.MemoryAuditor{}
	limiter, err := policy.NewWindowLimiter(100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := policy.NewDefault(
		clock, auditor, limiter, allowAnomalyDetector{},
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tools.NewManager(
		clock, tools.WithExecutionPolicy(pipeline),
	)
	if err != nil {
		t.Fatal(err)
	}
	actorID := uuid.New()
	sessionID := uuid.New()
	var pinCalls int
	if err := manager.Register(ctx, tools.Registration{
		Name: "memory_pin", Description: "Pin one memory.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"required":["id","pinned"],
			"properties":{
				"id":{"type":"string","format":"uuid"},
				"pinned":{"type":"boolean"}
			},
			"additionalProperties":false
		}`),
		Classification: tools.ClassificationYellow,
		Check:          func(context.Context) error { return nil },
		Handler: func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			scope, ok := controlplane.ApprovalScopeFromContext(runCtx)
			if !ok || scope.ActorID != actorID || scope.SessionID == nil ||
				*scope.SessionID != sessionID {
				return nil, errors.New("missing authenticated tool scope")
			}
			pinCalls++
			return append(json.RawMessage(nil), raw...), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	registry := swarm.NewRegistry(clock)
	first, err := registry.Spawn("", sessionID.String(), 0)
	if err != nil {
		t.Fatal(err)
	}
	otherSession := uuid.New()
	second, err := registry.Spawn("", otherSession.String(), 0)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := &productionCapabilities{
		manager: manager, policy: pipeline, swarmRegistry: registry,
	}
	service, err := NewSurfaceService(
		RuntimeInfo{StartedAt: time.Now()},
		capabilities,
	)
	if err != nil {
		t.Fatal(err)
	}
	scope := controlplane.Scope{
		ActorID: actorID, SessionID: &sessionID,
		Profile: "operator", Channel: "local",
	}
	memoryID := uuid.New()
	pinRequest := controlplane.Request{
		RequestID: uuid.New(), Kind: controlplane.KindCommand,
		Operation: controlplane.OperationMemoryPin, Scope: scope,
		IdempotencyKey: "pin-once",
		Payload: json.RawMessage(
			`{"id":"` + memoryID.String() + `","pinned":true}`,
		),
	}
	firstResult, err := service.Command(ctx, pinRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := service.Command(ctx, pinRequest)
	if err != nil {
		t.Fatal(err)
	}
	if pinCalls != 1 || string(firstResult) != string(secondResult) {
		t.Fatalf(
			"memory pin calls = %d, first = %s, second = %s",
			pinCalls,
			firstResult,
			secondResult,
		)
	}
	otherProfile := pinRequest
	otherProfile.RequestID = uuid.New()
	otherProfile.Scope.Profile = "secondary"
	if _, err := service.Command(ctx, otherProfile); err != nil {
		t.Fatal(err)
	}
	otherChannel := pinRequest
	otherChannel.RequestID = uuid.New()
	otherChannel.Scope.Channel = "telegram"
	if _, err := service.Command(ctx, otherChannel); err != nil {
		t.Fatal(err)
	}
	if pinCalls != 3 {
		t.Fatalf("profile/channel idempotency scopes were merged: calls = %d", pinCalls)
	}
	abortRequest := controlplane.Request{
		RequestID: uuid.New(), Kind: controlplane.KindCommand,
		Operation: controlplane.OperationSwarmAbort, Scope: scope,
		IdempotencyKey: "abort-first",
		Payload: json.RawMessage(
			`{"agent_id":"` + first.ID + `","expected_state":"running"}`,
		),
	}
	abortResult, err := service.Command(ctx, abortRequest)
	if err != nil || !strings.Contains(string(abortResult), `"state":"aborted"`) {
		t.Fatalf("abort result = %s, %v", abortResult, err)
	}
	crossSession := abortRequest
	crossSession.RequestID = uuid.New()
	crossSession.IdempotencyKey = "abort-cross-session"
	crossSession.Payload = json.RawMessage(
		`{"agent_id":"` + second.ID + `","expected_state":"running"}`,
	)
	var public controlplane.PublicError
	if _, err := service.Command(ctx, crossSession); !errors.As(err, &public) ||
		public.Code != controlplane.ErrorUnauthorized {
		t.Fatalf("cross-session abort error = %v", err)
	}
	events := auditor.Events()
	var memoryAudited, swarmAudited bool
	for _, event := range events {
		switch event.ToolName {
		case "memory_pin":
			memoryAudited = true
		case "swarm_abort":
			swarmAudited = true
		}
	}
	if !memoryAudited || !swarmAudited {
		t.Fatalf("policy events = %+v", events)
	}
}
