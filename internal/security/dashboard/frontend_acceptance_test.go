package dashboard_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/paxlabs-inc/ion-agent/internal/security/dashboard"
)

type dashboardClock struct{ at time.Time }

func (clock dashboardClock) Now() time.Time { return clock.at }

func TestDashboardFrontendAcceptanceEmbeddedAndLiveAuthenticatedStream(t *testing.T) {
	frontend, err := dashboard.Frontend()
	if err != nil {
		t.Fatal(err)
	}
	source, err := dashboard.New(dashboardClock{at: time.Unix(100, 0)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	const token = "dashboard-acceptance-token"
	stream, err := dashboard.NewServer(source, dashboard.ServerConfig{
		BearerToken: token, PingInterval: time.Second, PongTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/safety", stream)
	mux.Handle("/", frontend)
	server := httptest.NewServer(mux)
	defer server.Close()

	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), "Integrity console") ||
		!strings.Contains(response.Header.Get("Content-Security-Policy"), "object-src 'none'") {
		t.Fatalf("frontend response status=%d headers=%v body=%s",
			response.StatusCode, response.Header, body)
	}

	protocol := "ion-bearer." +
		base64.RawURLEncoding.EncodeToString([]byte(token))
	websocketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/safety"
	connection, _, err := websocket.DefaultDialer.Dial(
		websocketURL,
		http.Header{"Sec-WebSocket-Protocol": []string{protocol}},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if connection.Subprotocol() != protocol {
		t.Fatalf("selected subprotocol = %q", connection.Subprotocol())
	}
	var snapshot dashboard.StreamMessage
	if err := connection.ReadJSON(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Type != "snapshot" {
		t.Fatalf("first frame = %+v", snapshot)
	}
	source.Publish(
		dashboard.EventPolicyDecision, dashboard.SeverityWarning,
		"acceptance", "policy checked", map[string]bool{"allowed": false},
	)
	var frame dashboard.StreamMessage
	if err := connection.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != "event" || frame.Event == nil ||
		frame.Event.Message != "policy checked" {
		t.Fatalf("live frame = %+v", frame)
	}
}
