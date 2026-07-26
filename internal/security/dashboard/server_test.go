package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/paxlabs-inc/ion-agent/internal/reflection/cassandra"
	"github.com/paxlabs-inc/ion-agent/internal/security/canary"
	"github.com/paxlabs-inc/ion-agent/internal/security/circuit"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
)

const testBearerToken = "0123456789abcdef0123456789abcdef"

type cassandraRecorder struct {
	edits []cassandra.Edit
}

func (recorder *cassandraRecorder) RecordCassandraEvent(edit cassandra.Edit) error {
	recorder.edits = append(recorder.edits, edit)
	return nil
}

func Test_Server_AuthenticatedWebSocketStreamsSnapshotAndEvents(t *testing.T) {
	dashboard, _ := New(&testClock{baseTime}, 100)
	dashboard.Publish(
		EventPolicyDecision,
		SeverityInfo,
		"policy",
		"historical",
		nil,
	)
	server, err := NewServer(dashboard, ServerConfig{
		BearerToken:    testBearerToken,
		AllowedOrigins: []string{"https://dashboard.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	connection := dialDashboard(
		t,
		httpServer.URL+"?history=1",
		testBearerToken,
		"https://dashboard.example",
	)
	defer connection.Close()

	var snapshot StreamMessage
	if err := connection.ReadJSON(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Type != "snapshot" || len(snapshot.Events) != 1 ||
		snapshot.Events[0].Message != "historical" {
		t.Fatalf("snapshot = %+v", snapshot)
	}

	published := dashboard.Publish(
		EventCircuitBreaker,
		SeverityCritical,
		"circuit",
		"cross-axis",
		map[string]string{"action": "pause"},
	)
	var streamed StreamMessage
	if err := connection.ReadJSON(&streamed); err != nil {
		t.Fatal(err)
	}
	if streamed.Type != "event" || streamed.Event == nil ||
		streamed.Event.ID != published.ID ||
		streamed.Event.Timestamp.Format(time.RFC3339Nano) != baseTime.Format(time.RFC3339Nano) {
		t.Fatalf("streamed = %+v", streamed)
	}
}

func Test_Server_RejectsUnauthorizedOriginAndInvalidQueries(t *testing.T) {
	dashboard, _ := New(&testClock{baseTime}, 100)
	server, _ := NewServer(dashboard, ServerConfig{
		BearerToken:    testBearerToken,
		AllowedOrigins: []string{"https://dashboard.example"},
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	webSocketURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	tests := []struct {
		name   string
		url    string
		token  string
		origin string
		status int
	}{
		{
			name:   "missing bearer",
			url:    webSocketURL,
			origin: "https://dashboard.example",
			status: http.StatusUnauthorized,
		},
		{
			name:   "wrong bearer",
			url:    webSocketURL,
			token:  "wrong",
			origin: "https://dashboard.example",
			status: http.StatusUnauthorized,
		},
		{
			name:   "forbidden origin",
			url:    webSocketURL,
			token:  testBearerToken,
			origin: "https://evil.example",
			status: http.StatusForbidden,
		},
		{
			name:   "unknown filter",
			url:    webSocketURL + "?type=unknown",
			token:  testBearerToken,
			origin: "https://dashboard.example",
			status: http.StatusBadRequest,
		},
		{
			name:   "excess history",
			url:    webSocketURL + "?history=101",
			token:  testBearerToken,
			origin: "https://dashboard.example",
			status: http.StatusBadRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := http.Header{}
			if test.token != "" {
				header.Set("Authorization", "Bearer "+test.token)
			}
			if test.origin != "" {
				header.Set("Origin", test.origin)
			}
			connection, response, err := websocket.DefaultDialer.Dial(test.url, header)
			if connection != nil {
				connection.Close()
			}
			if err == nil {
				t.Fatal("unexpected successful WebSocket upgrade")
			}
			if response == nil || response.StatusCode != test.status {
				t.Fatalf("response = %+v, error = %v", response, err)
			}
		})
	}
}

func Test_Server_EnforcesConnectionPoolLimit(t *testing.T) {
	dashboard, _ := New(&testClock{baseTime}, 100)
	server, _ := NewServer(dashboard, ServerConfig{
		BearerToken:    testBearerToken,
		MaxConnections: 1,
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	first := dialDashboard(t, httpServer.URL, testBearerToken, "")
	defer first.Close()
	var snapshot StreamMessage
	if err := first.ReadJSON(&snapshot); err != nil {
		t.Fatal(err)
	}

	url := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	header := http.Header{"Authorization": []string{"Bearer " + testBearerToken}}
	second, response, err := websocket.DefaultDialer.Dial(url, header)
	if second != nil {
		second.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second connection response = %+v, error = %v", response, err)
	}
}

func Test_Server_ValidatesConfiguration(t *testing.T) {
	dashboard, _ := New(&testClock{baseTime}, 100)
	if _, err := NewServer(dashboard, ServerConfig{}); err == nil {
		t.Fatal("empty bearer token accepted")
	}
	if _, err := NewServer(dashboard, ServerConfig{
		BearerToken:    testBearerToken,
		MaxConnections: maximumWebSocketConnections + 1,
	}); err == nil {
		t.Fatal("excess connection pool accepted")
	}
	if _, err := NewServer(dashboard, ServerConfig{
		BearerToken:    testBearerToken,
		AllowedOrigins: []string{"file:///tmp/dashboard"},
	}); err == nil {
		t.Fatal("non-HTTP origin accepted")
	}
}

func Test_Bridges_SurfaceCircuitCanaryAndDurablePolicyEvents(t *testing.T) {
	dashboard, _ := New(&testClock{baseTime}, 100)
	subscription := dashboard.Subscribe()
	defer dashboard.Unsubscribe(subscription.ID)

	CircuitSink{Dashboard: dashboard}.CircuitBreakerTriggered(circuit.Event{
		At:       baseTime,
		Action:   circuit.ActionPauseAndAlertUser,
		Severity: circuit.SeverityCritical,
		Reason:   "resonance",
	})
	CanarySink{Dashboard: dashboard}.CanaryAlerted(canary.AlertEvent{
		CanaryID:  "canary-1",
		Operation: canary.OperationModify,
		Source:    "cortex",
		Timestamp: baseTime,
	})
	durable := &policy.MemoryAuditor{}
	auditor, err := NewPolicyAuditor(durable, dashboard)
	if err != nil {
		t.Fatal(err)
	}
	if err := auditor.RecordPolicyEvent(context.Background(), policy.AuditEvent{
		At:         baseTime,
		Layer:      policy.SandboxLayer,
		Decision:   policy.Deny,
		Reason:     "RED tool requires human approval",
		ToolCallID: "call-1",
		ToolName:   "payment",
		Arguments:  json.RawMessage(`{"amount":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	if len(durable.Events()) != 1 {
		t.Fatal("policy event was not durably recorded")
	}
	cassandraDurable := &cassandraRecorder{}
	cassandraAuditor, err := NewCassandraAuditor(cassandraDurable, dashboard)
	if err != nil {
		t.Fatal(err)
	}
	if err := cassandraAuditor.RecordCassandraEvent(cassandra.Edit{
		ID:            uuid.New(),
		OriginalMsgID: "message-1",
		Delta:         "+correction",
		Timestamp:     baseTime,
	}); err != nil {
		t.Fatal(err)
	}
	if len(cassandraDurable.edits) != 1 {
		t.Fatal("Cassandra edit was not durably recorded")
	}

	want := []EventType{
		EventCircuitBreaker,
		EventCanaryAccess,
		EventPolicyDecision,
		EventCassandraEdit,
	}
	for _, eventType := range want {
		select {
		case event := <-subscription.Events:
			if event.Type != eventType {
				t.Fatalf("event type = %s, want %s", event.Type, eventType)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for %s", eventType)
		}
	}
}

func dialDashboard(
	t *testing.T,
	httpURL string,
	token string,
	origin string,
) *websocket.Conn {
	t.Helper()
	webSocketURL := "ws" + strings.TrimPrefix(httpURL, "http")
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	if origin != "" {
		header.Set("Origin", origin)
	}
	connection, response, err := websocket.DefaultDialer.Dial(webSocketURL, header)
	if err != nil {
		t.Fatalf("Dial() error = %v, response = %+v", err, response)
	}
	return connection
}
