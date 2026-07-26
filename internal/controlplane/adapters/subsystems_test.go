package adapters

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type subsystemTestService struct {
	queries  []controlplane.Operation
	commands []controlplane.Operation
}

func (service *subsystemTestService) Query(
	_ context.Context,
	request controlplane.Request,
) (json.RawMessage, error) {
	service.queries = append(service.queries, request.Operation)
	return json.Marshal(map[string]any{
		"operation": request.Operation,
		"secret":    "must-not-leak",
	})
}

func (service *subsystemTestService) Command(
	_ context.Context,
	request controlplane.Request,
) (json.RawMessage, error) {
	service.commands = append(service.commands, request.Operation)
	return json.Marshal(map[string]any{"operation": request.Operation, "accepted": true})
}

func TestSubsystemHandlersExposeMatrixAndRouteMutations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	journal, err := controlplane.OpenJournal(
		ctx, ":memory:", types.SystemClock{}, controlplane.JournalConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	dispatcher, err := controlplane.NewDispatcher(
		journal,
		types.SystemClock{},
		controlplane.SnapshotFunc(func(context.Context, controlplane.Scope) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		}),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	service := &subsystemTestService{}
	var authorized []controlplane.Operation
	if err := RegisterSubsystemHandlers(
		dispatcher,
		service,
		MutationAuthorizerFunc(func(_ context.Context, request controlplane.Request) error {
			authorized = append(authorized, request.Operation)
			return nil
		}),
	); err != nil {
		t.Fatal(err)
	}
	actorID := uuid.New()
	for _, operation := range subsystemOperations {
		kind, _ := operation.Kind()
		request := controlplane.Request{
			ProtocolVersion: controlplane.ProtocolVersion,
			RequestID:       uuid.New(),
			Kind:            kind,
			Operation:       operation,
			Scope: controlplane.Scope{
				ActorID: actorID, Profile: "operator", Channel: "local",
			},
			Payload: json.RawMessage(`{}`),
		}
		if kind == controlplane.KindCommand {
			request.IdempotencyKey = uuid.NewString()
		}
		response := dispatcher.Dispatch(ctx, actorID, request)
		if response.Error != nil {
			t.Fatalf("%s response = %+v", operation, response.Error)
		}
		if operation == controlplane.OperationProviderList &&
			string(response.Result) != `{"operation":"provider.list","secret":"[REDACTED]"}` {
			t.Fatalf("provider response was not redacted: %s", response.Result)
		}
	}
	if len(service.queries)+len(service.commands) != len(subsystemOperations) {
		t.Fatalf(
			"boundary calls = %d, want %d",
			len(service.queries)+len(service.commands),
			len(subsystemOperations),
		)
	}
	if len(authorized) != len(service.commands) {
		t.Fatalf("authorized = %d, commands = %d", len(authorized), len(service.commands))
	}
	catalogResponse := dispatcher.Dispatch(ctx, actorID, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindQuery,
		Operation:       controlplane.OperationCommandsCatalog,
		Scope: controlplane.Scope{
			ActorID: actorID, Profile: "operator", Channel: "local",
		},
		Payload: json.RawMessage(`{}`),
	})
	if catalogResponse.Error != nil {
		t.Fatalf("catalog response = %+v", catalogResponse.Error)
	}
	var catalog []controlplane.CommandDescriptor
	if err := json.Unmarshal(catalogResponse.Result, &catalog); err != nil {
		t.Fatal(err)
	}
	availability := make(map[controlplane.Operation]bool, len(catalog))
	for _, descriptor := range catalog {
		availability[descriptor.Operation] = descriptor.Available
	}
	for _, operation := range []controlplane.Operation{
		controlplane.OperationPluginLifecycle,
		controlplane.OperationMCPReload,
		controlplane.OperationConfigPatch,
	} {
		if availability[operation] {
			t.Fatalf("%s was advertised as available", operation)
		}
	}
}
