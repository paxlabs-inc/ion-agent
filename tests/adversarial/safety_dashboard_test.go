package adversarial

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/paxlabs-inc/ion-agent/internal/security/circuit"
	"github.com/paxlabs-inc/ion-agent/internal/security/dashboard"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const dashboardToken = "abcdef0123456789abcdef0123456789"

func Test_SafetyDashboard_CriticalBreakerIsAuthenticatedAndUserVisible(t *testing.T) {
	events, err := dashboard.New(types.SystemClock{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	webSocketServer, err := dashboard.NewServer(events, dashboard.ServerConfig{
		BearerToken: dashboardToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(webSocketServer)
	defer httpServer.Close()
	webSocketURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	unauthorized, response, err := websocket.DefaultDialer.Dial(webSocketURL, nil)
	if unauthorized != nil {
		unauthorized.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized response = %+v, error = %v", response, err)
	}

	header := http.Header{"Authorization": []string{"Bearer " + dashboardToken}}
	connection, response, err := websocket.DefaultDialer.Dial(webSocketURL, header)
	if err != nil {
		t.Fatalf("authorized dial error = %v, response = %+v", err, response)
	}
	defer connection.Close()
	var snapshot dashboard.StreamMessage
	if err := connection.ReadJSON(&snapshot); err != nil {
		t.Fatal(err)
	}

	emotional := safety.NewEmotionalState()
	emotional.Update(0.71, 0.1, 0.71)
	config := circuit.DefaultBreakerConfig()
	config.EventSink = dashboard.CircuitSink{Dashboard: events}
	breaker, err := circuit.NewBreaker(config, emotional, types.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	result := breaker.Check()
	if result.Allowed || !result.AlertUser || !result.EmergencyReset {
		t.Fatalf("breaker result = %+v", result)
	}
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	var streamed dashboard.StreamMessage
	if err := connection.ReadJSON(&streamed); err != nil {
		t.Fatal(err)
	}
	if streamed.Event == nil ||
		streamed.Event.Type != dashboard.EventCircuitBreaker ||
		streamed.Event.Severity != dashboard.SeverityCritical {
		t.Fatalf("streamed event = %+v", streamed)
	}
}

func Test_IdleTimeEscalation_ExternalActionIsDenied(t *testing.T) {
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
		Name:                    "plugin_external_write",
		Description:             "External consequential plugin operation.",
		Parameters:              json.RawMessage(`{}`),
		Classification:          tools.ClassificationGreen,
		ExternallyCommunicating: true,
		Check:                   func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			executed = true
			return json.RawMessage(`null`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = manager.Execute(
		policy.WithPrincipal(context.Background(), policy.Principal{
			Sender:   policy.SenderAutomatrix,
			Approved: true,
		}),
		protocol.NormalizedToolCall{
			ID:        "idle-escalation",
			Name:      "plugin_external_write",
			Arguments: json.RawMessage(`{}`),
		},
	)
	if !errors.Is(err, policy.ErrDenied) || executed {
		t.Fatalf("idle escalation error = %v, executed = %v", err, executed)
	}
}
