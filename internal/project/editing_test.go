package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func TestPatchSetAtomicPreflightRevisionAndSymlinkDefense(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(attachRoot, "patches")
	writeIntelligenceFile(t, root, "a.txt", "before-a\n")
	writeIntelligenceFile(t, root, "b.txt", "before-b\n")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "patch-attach"), AttachInput{Name: "Patches", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	stale := PatchSet{Version: PatchSetVersion, ID: uuid.New(), ProjectID: project.ID,
		BaselineRevision: project.WorkspaceRevision, Criteria: []string{"atomic"}, ValidationPlan: []string{"test"},
		Members: []PatchMember{
			{Operation: PatchWrite, Path: "a.txt", ExpectedSHA256: testContentHash("before-a\n"), Content: "after-a\n"},
			{Operation: PatchWrite, Path: "b.txt", ExpectedSHA256: testContentHash("wrong"), Content: "after-b\n"},
		}}
	if _, err := service.ApplyPatchSet(ctx, actor, stale); !errors.Is(err, ErrStalePreimage) {
		t.Fatalf("stale patch = %v", err)
	}
	if content, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(content) != "before-a\n" {
		t.Fatalf("preflight mutated an earlier member: %q", content)
	}
	patch := stale
	patch.ID = uuid.New()
	patch.Members[1].ExpectedSHA256 = testContentHash("before-b\n")
	patch.Members = append(patch.Members, PatchMember{Operation: PatchWrite, Path: "created.txt",
		ExpectedSHA256: absentHash, Content: "created\n"})
	receipt, err := service.ApplyPatchSet(ctx, actor, patch)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "committed" || receipt.WorkspaceRevision != project.WorkspaceRevision+1 {
		t.Fatalf("patch receipt = %+v", receipt)
	}
	if _, err := service.ApplyPatchSet(ctx, actor, patch); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("replayed baseline = %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	writeIntelligenceFile(t, filepath.Dir(outside), filepath.Base(outside), "outside\n")
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	current, _ := service.Get(ctx, actor, project.ID)
	attack := PatchSet{Version: PatchSetVersion, ID: uuid.New(), ProjectID: project.ID,
		BaselineRevision: current.WorkspaceRevision, Criteria: []string{"safe"}, ValidationPlan: []string{"inspect"},
		Members: []PatchMember{{Operation: PatchWrite, Path: "link.txt", ExpectedSHA256: absentHash, Content: "escape"}}}
	if _, err := service.ApplyPatchSet(ctx, actor, attack); !errors.Is(err, ErrProtectedPath) {
		t.Fatalf("symlink patch = %v", err)
	}
	rollback := PatchSet{Version: PatchSetVersion, ID: uuid.New(), ProjectID: project.ID,
		BaselineRevision: current.WorkspaceRevision, Criteria: []string{"rollback"}, ValidationPlan: []string{"inspect"},
		Members: []PatchMember{
			{Operation: PatchWrite, Path: "a.txt", ExpectedSHA256: testContentHash("after-a\n"), Content: "must-rollback\n"},
			{Operation: PatchMkdir, Path: "missing/child", ExpectedSHA256: absentHash},
		}}
	if _, err := service.ApplyPatchSet(ctx, actor, rollback); err == nil {
		t.Fatal("inapplicable later member unexpectedly committed")
	}
	if content, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(content) != "after-a\n" {
		t.Fatalf("failed patch did not roll back byte-exactly: %q", content)
	}
	swapOutside := filepath.Join(t.TempDir(), "swap-outside.txt")
	if err := os.WriteFile(swapOutside, []byte("outside-safe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.editing.beforeApply = func() {
		if err := os.Remove(filepath.Join(root, "a.txt")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(swapOutside, filepath.Join(root, "a.txt")); err != nil {
			t.Fatal(err)
		}
	}
	swap := PatchSet{Version: PatchSetVersion, ID: uuid.New(), ProjectID: project.ID,
		BaselineRevision: current.WorkspaceRevision, Criteria: []string{"resist symlink swap"}, ValidationPlan: []string{"inspect outside bytes"},
		Members: []PatchMember{{Operation: PatchWrite, Path: "a.txt", ExpectedSHA256: testContentHash("after-a\n"), Content: "escaped\n"}}}
	if _, err := service.ApplyPatchSet(ctx, actor, swap); !errors.Is(err, ErrProtectedPath) {
		t.Fatalf("symlink swap patch = %v", err)
	}
	service.editing.beforeApply = nil
	if outside, _ := os.ReadFile(swapOutside); string(outside) != "outside-safe\n" {
		t.Fatalf("symlink swap escaped project root: %q", outside)
	}
	if err := os.Remove(filepath.Join(root, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	service.editing.beforeApply = func() {
		if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("human-concurrent\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	concurrent := PatchSet{Version: PatchSetVersion, ID: uuid.New(), ProjectID: project.ID,
		BaselineRevision: current.WorkspaceRevision, Criteria: []string{"preserve human work"}, ValidationPlan: []string{"compare exact bytes"},
		Members: []PatchMember{
			{Operation: PatchWrite, Path: "a.txt", ExpectedSHA256: testContentHash("after-a\n"), Content: "agent-partial\n"},
			{Operation: PatchWrite, Path: "b.txt", ExpectedSHA256: testContentHash("after-b\n"), Content: "agent-b\n"},
		}}
	if _, err := service.ApplyPatchSet(ctx, actor, concurrent); !errors.Is(err, ErrStalePreimage) {
		t.Fatalf("concurrent human patch = %v", err)
	}
	service.editing.beforeApply = nil
	if content, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(content) != "after-a\n" {
		t.Fatalf("agent partial mutation was not rolled back: %q", content)
	}
	if content, _ := os.ReadFile(filepath.Join(root, "b.txt")); string(content) != "human-concurrent\n" {
		t.Fatalf("concurrent human edit was discarded: %q", content)
	}
}

func TestPatchBinaryArchiveHistoryAndExactRollback(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(attachRoot, "binary-archive")
	writeIntelligenceFile(t, root, "source.txt", "archive me\n")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "binary-archive-attach"), AttachInput{Name: "Binary archive", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte{0, 1, 2, 0xff, 0, 7}
	patch := PatchSet{Version: PatchSetVersion, ID: uuid.New(), ProjectID: project.ID,
		BaselineRevision: project.WorkspaceRevision, Criteria: []string{"bounded media and archive"}, ValidationPlan: []string{"hash and roll back"},
		Members: []PatchMember{
			{Operation: PatchMkdir, Path: "empty", ExpectedSHA256: absentHash},
			{Operation: PatchWrite, Path: "blob.bin", ExpectedSHA256: absentHash,
				ContentBase64: base64.StdEncoding.EncodeToString(binary), MediaType: "application/octet-stream"},
			{Operation: PatchArchive, Path: "sources.tar.gz", ExpectedSHA256: absentHash, ArchivePaths: []string{"source.txt"}},
		}}
	if _, err := service.ApplyPatchSet(ctx, actor, patch); err == nil || !strings.Contains(err.Error(), "explicit approval") {
		t.Fatalf("unapproved binary patch = %v", err)
	}
	receipt, err := service.ApplyPatchSetApproved(ctx, actor, patch, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "blob.bin")); err != nil || !bytes.Equal(got, binary) {
		t.Fatalf("binary write = %x, %v", got, err)
	}
	archiveBefore, err := os.ReadFile(filepath.Join(root, "sources.tar.gz"))
	if err != nil || len(archiveBefore) == 0 {
		t.Fatalf("archive output: %v", err)
	}
	history, err := service.PatchHistory(ctx, actor, project.ID)
	if err != nil || len(history) != 1 || history[0].PatchSetID != receipt.PatchSetID || !history[0].RollbackAvailable {
		t.Fatalf("patch history = %+v, %v", history, err)
	}
	writeIntelligenceFile(t, root, "sources.tar.gz", "human edit")
	rollback := PatchRollbackRequest{ProjectID: project.ID, PatchSetID: receipt.PatchSetID, WorkspaceRevision: receipt.WorkspaceRevision}
	if _, err := service.RollbackPatchSet(ctx, actor, rollback); !errors.Is(err, ErrStalePreimage) {
		t.Fatalf("rollback over human edit = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sources.tar.gz"), archiveBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := service.RollbackPatchSet(ctx, actor, rollback)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.WorkspaceRevision != receipt.WorkspaceRevision+1 {
		t.Fatalf("rollback receipt = %+v", rolledBack)
	}
	for _, relative := range []string{"empty", "blob.bin", "sources.tar.gz"} {
		if _, err := os.Lstat(filepath.Join(root, relative)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rollback left %s: %v", relative, err)
		}
	}
}

func TestPatchJournalCrashReconciliationRestoresAllBefore(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(attachRoot, "crash")
	writeIntelligenceFile(t, root, "state.txt", "before-crash\n")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "crash-attach"), AttachInput{Name: "Crash", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	patch := PatchSet{Version: PatchSetVersion, ID: uuid.New(), ProjectID: project.ID,
		BaselineRevision: project.WorkspaceRevision, Criteria: []string{"recover"}, ValidationPlan: []string{"compare bytes"},
		Members: []PatchMember{{Operation: PatchWrite, Path: "state.txt", ExpectedSHA256: testContentHash("before-crash\n"), Content: "partial-after-crash\n"}}}
	before, err := preflightPatch(root, &patch)
	if err != nil {
		t.Fatal(err)
	}
	after, err := predictPatchPostimages(patch, before)
	if err != nil {
		t.Fatal(err)
	}
	journal := patchJournal{Version: PatchSetVersion, ActorID: actor, Patch: patch, State: "prepared", Before: before, After: after}
	if err := service.editing.save(ctx, actor, project.ID, journal); err != nil {
		t.Fatal(err)
	}
	writeIntelligenceFile(t, root, "state.txt", "partial-after-crash\n")
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, store = reopenProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	restarted, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(filepath.Join(root, "state.txt")); string(content) != "before-crash\n" {
		t.Fatalf("crash reconciliation = %q", content)
	}
}

func testContentHash(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func TestStructuredJSONPatchUsesExactPointerAndValidOutput(t *testing.T) {
	updated, err := applyJSONPointer([]byte(`{"service":{"ports":[3000,3001],"enabled":false}}`),
		"/service/ports/1", []byte(`4173`))
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "{\n  \"service\": {\n    \"enabled\": false,\n    \"ports\": [\n      3000,\n      4173\n    ]\n  }\n}\n" {
		t.Fatalf("structured JSON patch = %s", updated)
	}
	if _, err := applyJSONPointer(updated, "/service/missing/child", []byte(`true`)); err == nil {
		t.Fatal("structured patch created an unreviewed missing parent")
	}
	if _, err := applyJSONPointer(updated, "/service/~2escape", []byte(`true`)); err == nil {
		t.Fatal("invalid JSON pointer escape was accepted")
	}
}
