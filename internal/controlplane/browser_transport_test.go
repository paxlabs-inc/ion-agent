package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const browserTestOrigin = "https://operator.example"

func TestBrowserTransportRPCSecurityTicketReplayAndActorIsolation(t *testing.T) {
	ctx := context.Background()
	clock := &mutableClock{at: time.Unix(9_000, 0).UTC()}
	journal, err := OpenJournal(ctx, ":memory:", clock, JournalConfig{
		Retention: 64, SubscriberBuffer: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	dispatcher, err := NewDispatcher(
		journal,
		clock,
		SnapshotFunc(func(context.Context, Scope) (json.RawMessage, error) {
			return json.RawMessage(`{"pending_approvals":[],"secret":"hidden"}`), nil
		}),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.New()
	if err := dispatcher.Register(
		OperationSessionCreate,
		"Create a session through its application service.",
		HandlerFunc(func(context.Context, Request, EventEmitter) (json.RawMessage, error) {
			return json.RawMessage(`{"session_id":"` + sessionID.String() + `"}`), nil
		}),
	); err != nil {
		t.Fatal(err)
	}
	authenticator, err := NewCookieAuthenticator(
		[]byte("01234567890123456789012345678901"),
		clock,
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	tickets, err := NewTicketManager(clock, 30*time.Second, 16)
	if err != nil {
		t.Fatal(err)
	}
	browser, err := NewBrowserServer(
		dispatcher,
		journal,
		authenticator,
		tickets,
		clock,
		BrowserServerConfig{
			AllowedOrigins:  []string{browserTestOrigin},
			MaxPayloadBytes: 4096, RequestsPerMinute: 20,
			PingInterval: time.Second, PongTimeout: 3 * time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(browser)
	defer server.Close()

	actorID := uuid.New()
	sessionCookie, csrfCookie, err := authenticator.Issue(actorID)
	if err != nil {
		t.Fatal(err)
	}
	query := Request{
		ProtocolVersion: ProtocolVersion, RequestID: uuid.New(),
		Kind: KindQuery, Operation: OperationCommandsCatalog,
		Scope: Scope{ActorID: actorID}, Payload: json.RawMessage(`{}`),
	}
	queryResponse := postRPC(t, server.URL, query, sessionCookie, csrfCookie, "", "")
	if queryResponse.StatusCode != http.StatusOK {
		t.Fatalf("query status = %d body=%s", queryResponse.StatusCode, readBody(t, queryResponse))
	}
	_ = queryResponse.Body.Close()

	command := validCommand(actorID, OperationSessionCreate, "browser-create", `{}`)
	for _, test := range []struct {
		name   string
		origin string
		csrf   string
	}{
		{name: "missing origin", csrf: csrfCookie.Value},
		{name: "foreign origin", origin: "https://attacker.example", csrf: csrfCookie.Value},
		{name: "missing csrf", origin: browserTestOrigin},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := postRPC(
				t, server.URL, command, sessionCookie, csrfCookie, test.origin, test.csrf,
			)
			defer response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d body=%s", response.StatusCode, readBody(t, response))
			}
		})
	}
	commandResponse := postRPC(
		t, server.URL, command, sessionCookie, csrfCookie,
		browserTestOrigin, csrfCookie.Value,
	)
	if commandResponse.StatusCode != http.StatusOK {
		t.Fatalf("command status = %d body=%s",
			commandResponse.StatusCode, readBody(t, commandResponse))
	}
	_ = commandResponse.Body.Close()

	ticket := issueBrowserTicket(
		t, server.URL, sessionCookie, csrfCookie, browserTestOrigin,
	)
	websocketURL := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/v1/events?after=0&ticket=" + urlQueryEscape(ticket.Value)
	connection, response, err := websocket.DefaultDialer.Dial(
		websocketURL,
		http.Header{"Origin": []string{browserTestOrigin}},
	)
	if err != nil {
		if response != nil {
			t.Fatalf("websocket dial: %v body=%s", err, readBody(t, response))
		}
		t.Fatal(err)
	}
	defer connection.Close()
	var recoveryFrame StreamFrame
	if err := connection.ReadJSON(&recoveryFrame); err != nil {
		t.Fatal(err)
	}
	if recoveryFrame.Type != "recovery" || recoveryFrame.Recovery == nil ||
		string(recoveryFrame.Recovery.Snapshot) !=
			`{"pending_approvals":[],"secret":"[REDACTED]"}` {
		t.Fatalf("recovery frame = %+v", recoveryFrame)
	}

	otherActor := uuid.New()
	if _, err := journal.Append(ctx, newJournalEvent(t, otherActor, 90)); err != nil {
		t.Fatal(err)
	}
	actorEvent := newJournalEvent(t, actorID, 91)
	storedActorEvent, err := journal.Append(ctx, actorEvent)
	if err != nil {
		t.Fatal(err)
	}
	var eventFrame StreamFrame
	if err := connection.ReadJSON(&eventFrame); err != nil {
		t.Fatal(err)
	}
	if eventFrame.Type != "event" || eventFrame.Event == nil ||
		eventFrame.Event.Sequence != storedActorEvent.Sequence ||
		eventFrame.Event.Correlation.ActorID != actorID {
		t.Fatalf("actor-scoped event frame = %+v", eventFrame)
	}
	if err := connection.WriteJSON(clientStreamFrame{
		Type: "ack", ClientID: "browser-one", Sequence: storedActorEvent.Sequence,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		acknowledged, err := journal.Acknowledged(ctx, actorID, "browser-one")
		if err != nil {
			t.Fatal(err)
		}
		if acknowledged == storedActorEvent.Sequence {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("acknowledgement remained %d", acknowledged)
		}
		time.Sleep(time.Millisecond)
	}

	replayedConnection, replayResponse, replayErr := websocket.DefaultDialer.Dial(
		websocketURL,
		http.Header{"Origin": []string{browserTestOrigin}},
	)
	if replayedConnection != nil {
		_ = replayedConnection.Close()
	}
	if replayErr == nil || replayResponse == nil ||
		replayResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ticket replay connection=%v response=%v error=%v",
			replayedConnection, replayResponse, replayErr)
	}
	_ = replayResponse.Body.Close()

	expired := issueBrowserTicket(
		t, server.URL, sessionCookie, csrfCookie, browserTestOrigin,
	)
	clock.Advance(time.Minute)
	expiredURL := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/v1/events?ticket=" + urlQueryEscape(expired.Value)
	expiredConnection, expiredResponse, expiredErr := websocket.DefaultDialer.Dial(
		expiredURL,
		http.Header{"Origin": []string{browserTestOrigin}},
	)
	if expiredConnection != nil {
		_ = expiredConnection.Close()
	}
	if expiredErr == nil || expiredResponse == nil ||
		expiredResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired ticket connection=%v response=%v error=%v",
			expiredConnection, expiredResponse, expiredErr)
	}
	_ = expiredResponse.Body.Close()
}

func TestBrowserTransportPaginatesCatchUpPastTwoThousandWithoutCursorJump(t *testing.T) {
	ctx := context.Background()
	clock := &mutableClock{at: time.Unix(9_500, 0).UTC()}
	journal, err := OpenJournal(ctx, ":memory:", clock, JournalConfig{
		Retention: 3_000, SubscriberBuffer: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	actorID := uuid.New()
	approvalID := uuid.New()
	for index := 0; index < maxReplayEvents; index++ {
		if _, err := journal.Append(ctx, newJournalEvent(t, actorID, index)); err != nil {
			t.Fatal(err)
		}
	}
	terminal, err := NewEvent(
		EventTurnCompleted, Correlation{ActorID: actorID},
		json.RawMessage(`{"state":"completed"}`), clock.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	storedTerminal, err := journal.Append(ctx, terminal)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewEvent(
		EventApprovalRequested, Correlation{ActorID: actorID},
		json.RawMessage(`{"approval_id":"`+approvalID.String()+`","operation":"release"}`),
		clock.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	storedApproval, err := journal.Append(ctx, approval)
	if err != nil {
		t.Fatal(err)
	}

	dispatcher, err := NewDispatcher(
		journal, clock,
		SnapshotFunc(func(context.Context, Scope) (json.RawMessage, error) {
			return json.RawMessage(`{"active_turns":[],"pending_approvals":[]}`), nil
		}), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := NewCookieAuthenticator(
		[]byte("01234567890123456789012345678901"), clock, time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	tickets, err := NewTicketManager(clock, 30*time.Second, 8)
	if err != nil {
		t.Fatal(err)
	}
	browser, err := NewBrowserServer(
		dispatcher, journal, authenticator, tickets, clock,
		BrowserServerConfig{
			AllowedOrigins: []string{browserTestOrigin}, ReplayLimit: maxReplayEvents,
			PingInterval: time.Second, PongTimeout: 3 * time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(browser)
	defer server.Close()
	sessionCookie, csrfCookie, err := authenticator.Issue(actorID)
	if err != nil {
		t.Fatal(err)
	}
	ticket := issueBrowserTicket(
		t, server.URL, sessionCookie, csrfCookie, browserTestOrigin,
	)
	websocketURL := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/v1/events?after=0&ticket=" + urlQueryEscape(ticket.Value)
	connection, response, err := websocket.DefaultDialer.Dial(
		websocketURL, http.Header{"Origin": []string{browserTestOrigin}},
	)
	if err != nil {
		if response != nil {
			t.Fatalf("websocket dial: %v body=%s", err, readBody(t, response))
		}
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))

	var first StreamFrame
	if err := connection.ReadJSON(&first); err != nil {
		t.Fatal(err)
	}
	if first.Type != "recovery" || first.Recovery == nil ||
		len(first.Recovery.Replay.Events) != maxReplayEvents ||
		first.Recovery.Replay.Latest != maxReplayEvents ||
		first.Recovery.Replay.Head != storedApproval.Sequence ||
		len(first.Recovery.Snapshot) == 0 {
		t.Fatalf("first catch-up frame = type:%s replay:%+v snapshot:%s",
			first.Type, first.Recovery, first.Recovery.Snapshot)
	}
	var second StreamFrame
	if err := connection.ReadJSON(&second); err != nil {
		t.Fatal(err)
	}
	if second.Type != "recovery" || second.Recovery == nil ||
		len(second.Recovery.Replay.Events) != 2 ||
		second.Recovery.Replay.Latest != storedApproval.Sequence ||
		second.Recovery.Replay.Head != storedApproval.Sequence ||
		len(second.Recovery.Snapshot) != 0 ||
		second.Recovery.Replay.Events[0].Sequence != storedTerminal.Sequence ||
		second.Recovery.Replay.Events[1].Sequence != storedApproval.Sequence {
		t.Fatalf("second catch-up frame = %+v", second)
	}
	if err := connection.WriteJSON(clientStreamFrame{
		Type: "ack", ClientID: "browser-paged", Sequence: storedApproval.Sequence,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		acknowledged, err := journal.Acknowledged(ctx, actorID, "browser-paged")
		if err != nil {
			t.Fatal(err)
		}
		if acknowledged == storedApproval.Sequence {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("paged acknowledgement remained %d", acknowledged)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBrowserTransportFailsClosedForAuthPayloadAndNonTLSRemote(t *testing.T) {
	ctx := context.Background()
	clock := &mutableClock{at: time.Unix(10_000, 0).UTC()}
	journal, err := OpenJournal(ctx, ":memory:", clock, JournalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	dispatcher, err := NewDispatcher(
		journal, clock,
		SnapshotFunc(func(context.Context, Scope) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		}), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := NewCookieAuthenticator(
		[]byte("01234567890123456789012345678901"), clock, time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	tickets, err := NewTicketManager(clock, 30*time.Second, 8)
	if err != nil {
		t.Fatal(err)
	}
	browser, err := NewBrowserServer(
		dispatcher, journal, authenticator, tickets, clock,
		BrowserServerConfig{
			AllowedOrigins:  []string{browserTestOrigin},
			MaxPayloadBytes: 256,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRequest(http.MethodPost, "/v1/rpc", strings.NewReader(`{}`))
	unauthorized.RemoteAddr = "127.0.0.1:1234"
	unauthorizedRecorder := httptest.NewRecorder()
	browser.ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthorizedRecorder.Code)
	}

	actorID := uuid.New()
	sessionCookie, csrfCookie, err := authenticator.Issue(actorID)
	if err != nil {
		t.Fatal(err)
	}
	oversized := httptest.NewRequest(
		http.MethodPost, "/v1/rpc", strings.NewReader(strings.Repeat("x", 300)),
	)
	oversized.RemoteAddr = "127.0.0.1:1234"
	oversized.AddCookie(sessionCookie)
	oversized.AddCookie(csrfCookie)
	oversizedRecorder := httptest.NewRecorder()
	browser.ServeHTTP(oversizedRecorder, oversized)
	if oversizedRecorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized status = %d", oversizedRecorder.Code)
	}

	nonTLS := httptest.NewRequest(http.MethodPost, "/v1/rpc", strings.NewReader(`{}`))
	nonTLS.RemoteAddr = "203.0.113.10:1234"
	nonTLSRecorder := httptest.NewRecorder()
	browser.ServeHTTP(nonTLSRecorder, nonTLS)
	if nonTLSRecorder.Code != http.StatusUpgradeRequired {
		t.Fatalf("non-TLS remote status = %d", nonTLSRecorder.Code)
	}
}

func postRPC(
	t *testing.T,
	serverURL string,
	request Request,
	sessionCookie *http.Cookie,
	csrfCookie *http.Cookie,
	origin string,
	csrf string,
) *http.Response {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest, err := http.NewRequest(
		http.MethodPost, serverURL+"/v1/rpc", bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.AddCookie(sessionCookie)
	httpRequest.AddCookie(csrfCookie)
	if origin != "" {
		httpRequest.Header.Set("Origin", origin)
	}
	if csrf != "" {
		httpRequest.Header.Set(CSRFHeaderName, csrf)
	}
	response, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func issueBrowserTicket(
	t *testing.T,
	serverURL string,
	sessionCookie *http.Cookie,
	csrfCookie *http.Cookie,
	origin string,
) Ticket {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, serverURL+"/v1/ws-ticket", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set("Origin", origin)
	request.Header.Set(CSRFHeaderName, csrfCookie.Value)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("ticket status = %d body=%s", response.StatusCode, readBody(t, response))
	}
	var ticket Ticket
	if err := json.NewDecoder(response.Body).Decode(&ticket); err != nil {
		t.Fatal(err)
	}
	return ticket
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func urlQueryEscape(value string) string {
	return value
}
