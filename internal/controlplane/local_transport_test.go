//go:build !windows

package controlplane

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLocalTransportCapabilityRPCStreamingIsolationAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := &mutableClock{at: time.Unix(11_000, 0).UTC()}
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
			return json.RawMessage(`{"connection":"local"}`), nil
		}),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := NewCapabilityManager(clock, 30*time.Second, 8)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(directory, "controlplane.sock")
	server, err := NewLocalServer(
		dispatcher,
		journal,
		capabilities,
		nil,
		LocalServerConfig{
			SocketPath: socketPath, MaxConnections: 2,
			MaxPayloadBytes: 4096, ReadTimeout: 2 * time.Second,
			WriteTimeout: time.Second, ReplayLimit: 2,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.ListenAndServe(ctx) }()
	waitForSocket(t, socketPath)
	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %o", info.Mode().Perm())
	}

	actorID := uuid.New()
	capability, err := capabilities.Issue(actorID)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialUnix(
		"unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"},
	)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(connection)
	decoder := json.NewDecoder(connection)
	if err := encoder.Encode(localHandshake{Capability: capability.Value}); err != nil {
		t.Fatal(err)
	}
	var ready LocalServerFrame
	if err := decoder.Decode(&ready); err != nil {
		t.Fatal(err)
	}
	if ready.Type != "ready" || ready.ActorID == nil || *ready.ActorID != actorID {
		t.Fatalf("ready frame = %+v", ready)
	}

	request := Request{
		ProtocolVersion: ProtocolVersion, RequestID: uuid.New(),
		Kind: KindQuery, Operation: OperationCommandsCatalog,
		Scope: Scope{ActorID: actorID}, Payload: json.RawMessage(`{}`),
	}
	if err := encoder.Encode(localClientFrame{Type: "rpc", Request: &request}); err != nil {
		t.Fatal(err)
	}
	var rpc LocalServerFrame
	if err := decoder.Decode(&rpc); err != nil {
		t.Fatal(err)
	}
	if rpc.Type != "rpc" || rpc.Response == nil || rpc.Response.Error != nil {
		t.Fatalf("RPC frame = %+v", rpc)
	}

	for index := 0; index < 3; index++ {
		if _, err := journal.Append(ctx, newJournalEvent(t, actorID, 110+index)); err != nil {
			t.Fatal(err)
		}
	}
	if err := encoder.Encode(localClientFrame{Type: "subscribe"}); err != nil {
		t.Fatal(err)
	}
	var recovery LocalServerFrame
	if err := decoder.Decode(&recovery); err != nil {
		t.Fatal(err)
	}
	if recovery.Type != "recovery" || recovery.Recovery == nil ||
		string(recovery.Recovery.Snapshot) != `{"connection":"local"}` ||
		len(recovery.Recovery.Replay.Events) != 2 ||
		recovery.Recovery.Replay.Latest != 2 || recovery.Recovery.Replay.Head != 3 {
		t.Fatalf("recovery frame = %+v", recovery)
	}
	var catchUp LocalServerFrame
	if err := decoder.Decode(&catchUp); err != nil {
		t.Fatal(err)
	}
	if catchUp.Type != "recovery" || catchUp.Recovery == nil ||
		len(catchUp.Recovery.Snapshot) != 0 ||
		len(catchUp.Recovery.Replay.Events) != 1 ||
		catchUp.Recovery.Replay.Latest != 3 || catchUp.Recovery.Replay.Head != 3 {
		t.Fatalf("catch-up frame = %+v", catchUp)
	}
	if _, err := journal.Append(ctx, newJournalEvent(t, uuid.New(), 120)); err != nil {
		t.Fatal(err)
	}
	stored, err := journal.Append(ctx, newJournalEvent(t, actorID, 121))
	if err != nil {
		t.Fatal(err)
	}
	var eventFrame LocalServerFrame
	if err := decoder.Decode(&eventFrame); err != nil {
		t.Fatal(err)
	}
	if eventFrame.Type != "event" || eventFrame.Event == nil ||
		eventFrame.Event.Sequence != stored.Sequence ||
		eventFrame.Event.Correlation.ActorID != actorID {
		t.Fatalf("local actor event = %+v", eventFrame)
	}
	if err := encoder.Encode(localClientFrame{
		Type: "ack", ClientID: "tui-one", Sequence: stored.Sequence,
	}); err != nil {
		t.Fatal(err)
	}
	var acknowledgement LocalServerFrame
	if err := decoder.Decode(&acknowledgement); err != nil {
		t.Fatal(err)
	}
	if acknowledgement.Type != "ack" || acknowledgement.Acknowledged == nil ||
		*acknowledgement.Acknowledged != stored.Sequence {
		t.Fatalf("acknowledgement frame = %+v", acknowledgement)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}

	replayConnection, err := net.DialUnix(
		"unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"},
	)
	if err != nil {
		t.Fatal(err)
	}
	replayEncoder := json.NewEncoder(replayConnection)
	replayDecoder := json.NewDecoder(replayConnection)
	if err := replayEncoder.Encode(localHandshake{Capability: capability.Value}); err != nil {
		t.Fatal(err)
	}
	var rejected LocalServerFrame
	if err := replayDecoder.Decode(&rejected); err != nil {
		t.Fatal(err)
	}
	_ = replayConnection.Close()
	if rejected.Type != "error" || rejected.Error == nil ||
		rejected.Error.Code != ErrorUnauthorized {
		t.Fatalf("replayed capability frame = %+v", rejected)
	}

	cancel()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("local server did not stop after cancellation")
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("owned socket remains after shutdown: %v", err)
	}
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if info, err := os.Stat(socketPath); err == nil && info.Mode().Perm() == 0o600 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("local socket was not created")
		}
		time.Sleep(time.Millisecond)
	}
}
