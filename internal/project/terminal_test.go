//go:build linux

package project

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controllease"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func TestSupervisedProcessPTYReplayRedactionFloodAndCancellation(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(attachRoot, "terminal")
	writeIntelligenceFile(t, root, "README.md", "terminal fixture\n")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "terminal-attach"), AttachInput{Name: "Terminal", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	oneShot, err := service.StartProcess(ctx, actor, ProcessRequest{ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, Mode: ProcessOneShot,
		Argv: []string{"/usr/bin/printf", "token=terminal-secret-value\\n"}, OutputBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	replay := waitTerminal(t, service, actor, oneShot.ID, "completed")
	if strings.Contains(replay.Output, "terminal-secret-value") || !strings.Contains(replay.Output, "[REDACTED]") {
		t.Fatalf("terminal redaction = %q", replay.Output)
	}
	if _, err := service.ReplayTerminal(ctx, uuid.New(), oneShot.ID, 0); !errors.Is(err, ErrTerminalNotFound) {
		t.Fatalf("cross-actor replay = %v", err)
	}

	pty, err := service.StartProcess(ctx, actor, ProcessRequest{ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, Mode: ProcessPTY,
		Argv:        []string{"/bin/sh", "-c", "read value; echo received:$value; sleep 30 & echo child:$!; wait"},
		OutputBytes: 8192, Columns: 80, Rows: 24, TimeoutSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResizeTerminal(ctx, actor, pty.ID, 100, 30); err != nil {
		t.Fatal(err)
	}
	if err := service.WriteTerminal(ctx, actor, pty.ID, []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		replay, err = service.ReplayTerminal(ctx, actor, pty.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(replay.Output, "received:hello") && strings.Contains(replay.Output, "child:") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("PTY output = %q", replay.Output)
		}
		time.Sleep(10 * time.Millisecond)
	}
	childText := strings.TrimSpace(strings.Split(strings.Split(replay.Output, "child:")[1], "\n")[0])
	childPID, err := strconv.Atoi(childText)
	if err != nil {
		t.Fatalf("child PID in %q: %v", replay.Output, err)
	}
	if err := service.SignalTerminal(ctx, actor, pty.ID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, service, actor, pty.ID, "failed")
	deadline = time.Now().Add(2 * time.Second)
	for syscall.Kill(childPID, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(childPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child process %d survived terminal process-group signal: %v", childPID, err)
	}

	flood, err := service.StartProcess(ctx, actor, ProcessRequest{ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, Mode: ProcessOneShot,
		Argv: []string{"/usr/bin/yes", "bounded"}, OutputBytes: 4096, TimeoutSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	replay = waitTerminal(t, service, actor, flood.ID, "timed_out")
	if !replay.State.Truncated || len(replay.Output) > 4096 || replay.State.DroppedBytes == 0 {
		t.Fatalf("flood bounds = %+v, bytes=%d", replay.State, len(replay.Output))
	}
	cancelled, err := service.StartProcess(ctx, actor, ProcessRequest{ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, Mode: ProcessOneShot,
		Argv: []string{"/bin/sleep", "30"}, OutputBytes: 4096, TimeoutSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CancelTerminal(ctx, actor, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, service, actor, cancelled.ID, "cancelled")
}

func TestTerminalTakeoverLeaseOwnsRealPTYInputAndReconciles(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	control, err := controllease.New(store, types.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		store,
		types.SystemClock{},
		ServiceConfig{
			WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot},
			Control: control,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(attachRoot, "controlled-terminal")
	writeIntelligenceFile(t, root, "README.md", "controlled terminal\n")
	actorID, sessionID, turnID := uuid.New(), uuid.New(), uuid.New()
	project, err := service.AttachDirectory(
		ctx,
		testMeta(actorID, "controlled-terminal-attach"),
		AttachInput{Name: "Controlled terminal", Directory: root},
	)
	if err != nil {
		t.Fatal(err)
	}
	runCtx := controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
		ActorID: actorID, SessionID: &sessionID, TurnID: &turnID,
		TaskID: &turnID, AgentID: "ion",
	})
	terminal, err := service.StartProcess(
		runCtx,
		actorID,
		ProcessRequest{
			ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
			Mode: ProcessPTY, Argv: []string{"/bin/sh", "-c", "read value; echo controlled:$value; sleep 30"},
			OutputBytes: 8192, Columns: 80, Rows: 24, TimeoutSeconds: 60,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	target, owner, err := service.TerminalControlBinding(ctx, actorID, terminal.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := control.Acquire(
		ctx, target, owner, 0, controllease.MinimumLeaseTTL,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.WriteTerminal(
		runCtx, actorID, terminal.ID, []byte("automation\n"),
	); !errors.Is(err, controllease.ErrHeld) {
		t.Fatalf("executor input during takeover error = %v", err)
	}
	if err := service.WriteTerminalWithLease(
		ctx, actorID, terminal.ID, lease.ID, lease.Revision,
		[]byte("operator\n"),
	); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		replay, replayErr := service.ReplayTerminal(ctx, actorID, terminal.ID, 0)
		if replayErr != nil {
			t.Fatal(replayErr)
		}
		if strings.Contains(replay.Output, "controlled:operator") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("controlled PTY output = %q", replay.Output)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := service.terminals.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	reconciled, err := control.Status(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.State != controllease.StateReleased ||
		reconciled.Authority != controllease.AuthorityExecutor {
		t.Fatalf("restart-reconciled terminal lease = %+v", reconciled)
	}
	waitTerminal(t, service, actorID, terminal.ID, "failed")
}

func waitTerminal(t *testing.T, service *Service, actor, id uuid.UUID, status string) TerminalReplay {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		replay, err := service.ReplayTerminal(context.Background(), actor, id, 0)
		if err != nil {
			t.Fatal(err)
		}
		if replay.State.Status == status {
			return replay
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal %s status = %s", id, replay.State.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
