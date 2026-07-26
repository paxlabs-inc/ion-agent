package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func TestGitProjectionPreservesDirtyTreeAndCapturesStableBaseline(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(attachRoot, "git-projection")
	writeIntelligenceFile(t, root, "tracked.txt", "first\nsecond\n")
	writeIntelligenceFile(t, root, "rename-me.txt", "rename content\n")
	runGit(t, "init", "--initial-branch=main", root)
	runGit(t, "-C", root, "add", ".")
	runGitWithIdentity(t, "-C", root, "commit", "-m", "baseline")
	runGit(t, "-C", root, "remote", "add", "origin", "https://example.invalid/owner/repo.git")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("first\nchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "rename-me.txt"), filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0, 1, 0xff, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", root, "add", "binary.bin", "rename-me.txt", "renamed.txt")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "git-projection-attach"), AttachInput{Name: "Git projection", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := service.GitProjection(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Branch != "main" || projection.Detached || projection.Head == "" || len(projection.History) != 1 ||
		len(projection.Remotes) != 1 || projection.Remotes[0].Name != "origin" || projection.Baseline.StatusSHA256 == "" {
		t.Fatalf("Git projection = %+v", projection)
	}
	if !strings.Contains(projection.UnstagedDiff, "changed") || !strings.Contains(projection.StagedDiff, "binary.bin") {
		t.Fatalf("Git diffs staged=%q unstaged=%q", projection.StagedDiff, projection.UnstagedDiff)
	}
	baseline := projection.Baseline
	if err := os.WriteFile(filepath.Join(root, "later-untracked.txt"), []byte("later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := service.GitProjection(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Baseline != baseline {
		t.Fatalf("read-only baseline drifted: before=%+v after=%+v", baseline, second.Baseline)
	}
	if _, err := service.GitProjection(ctx, uuid.New(), project.ID); err == nil {
		t.Fatal("cross-actor Git projection was accepted")
	}
	blame, err := service.GitBlame(ctx, actor, GitBlameRequest{ProjectID: project.ID, Path: "tracked.txt", StartLine: 1, EndLine: 2})
	if err != nil || len(blame) != 2 || blame[0].Commit == "" {
		t.Fatalf("Git blame = %+v, %v", blame, err)
	}
	status, err := os.ReadFile(filepath.Join(root, "tracked.txt"))
	if err != nil || string(status) != "first\nchanged\n" {
		t.Fatalf("read-only Git projection altered dirty work: %q, %v", status, err)
	}
	review, err := service.BuildGitReview(ctx, actor, GitReviewRequest{ProjectID: project.ID,
		CriteriaByPath: map[string]string{"tracked.txt": "criterion.behavior", "binary.bin": "criterion.asset"}})
	if err != nil || len(review.Groups) < 2 {
		t.Fatalf("criterion-grouped review = %+v, %v", review, err)
	}
	foundBinary := false
	for _, group := range review.Groups {
		for _, file := range group.Files {
			if file.Path == "binary.bin" && containsString(file.Kinds, "binary") && group.Criterion == "criterion.asset" {
				foundBinary = true
			}
		}
	}
	if !foundBinary {
		t.Fatalf("binary review classification = %+v", review.Groups)
	}
	comment, err := service.AddGitReviewComment(ctx, actor, GitReviewCommentInput{ProjectID: project.ID,
		Path: "tracked.txt", Line: 2, Criterion: "criterion.behavior", Body: "Preserve this review concern."})
	if err != nil {
		t.Fatal(err)
	}
	comments, err := service.ListGitReviewComments(ctx, actor, project.ID)
	if err != nil || len(comments) != 1 || comments[0].ID != comment.ID || comments[0].ResolvedAt != nil {
		t.Fatalf("durable review comments = %+v, %v", comments, err)
	}
	resolved, err := service.ResolveGitReviewComment(ctx, actor, GitReviewResolveInput{ProjectID: project.ID, CommentID: comment.ID})
	if err != nil || resolved.ResolvedAt == nil {
		t.Fatalf("resolved review work item = %+v, %v", resolved, err)
	}
}

func TestGitPartialHunkStagingAndRefCrashReconciliation(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(attachRoot, "git-hunks")
	writeIntelligenceFile(t, root, "partial.txt", "one\nkeep\nkeep\ntwo\n")
	runGit(t, "init", "--initial-branch=main", root)
	runGit(t, "-C", root, "add", ".")
	runGitWithIdentity(t, "-C", root, "commit", "-m", "baseline")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "git-hunks-attach"), AttachInput{Name: "Git hunks", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	projection, _ := service.GitProjection(ctx, actor, project.ID)
	content := "ONE\nkeep\nkeep\nTWO\n"
	if err := os.WriteFile(filepath.Join(root, "partial.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	full, _, err := runGitBounded(ctx, root, "diff", "--binary", "--no-ext-diff", "--", "partial.txt")
	if err != nil {
		t.Fatal(err)
	}
	patch := "diff --git a/partial.txt b/partial.txt\n--- a/partial.txt\n+++ b/partial.txt\n@@ -1 +1 @@\n-one\n+ONE\n"
	receipt, err := service.StageGitHunks(ctx, actor, GitStageHunksRequest{ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, ExpectedHead: projection.Head, DiffSHA256: byteDigest(full),
		Patch: patch, Paths: []GitPathExpectation{{Path: "partial.txt", SHA256: testContentHash(content)}}})
	if err != nil || receipt.Operation != "stage.hunks" {
		t.Fatalf("partial stage = %+v, %v", receipt, err)
	}
	staged, _, _ := runGitBounded(ctx, root, "diff", "--cached")
	unstaged, _, _ := runGitBounded(ctx, root, "diff")
	if !strings.Contains(string(staged), "+ONE") || strings.Contains(string(staged), "+TWO") ||
		!strings.Contains(string(unstaged), "+TWO") {
		t.Fatalf("partial staging staged=%q unstaged=%q", staged, unstaged)
	}

	current, _ := service.Get(ctx, actor, project.ID)
	head, _ := exactGitHead(ctx, root)
	journal := gitMutationJournal{Version: GitContractVersion, ActorID: actor, ProjectID: project.ID,
		WorkspaceRevision: current.WorkspaceRevision, Operation: "branch.create", BeforeHead: head,
		BeforeStatus: "irrelevant-after-ref-effect", IntendedRef: "refs/heads/recovered", IntendedTarget: head, State: "prepared"}
	if err := service.saveGitMutation(ctx, journal); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", root, "branch", "recovered", head)
	if err := service.reconcileGitMutations(ctx); err != nil {
		t.Fatal(err)
	}
	reconciled, _ := service.Get(ctx, actor, project.ID)
	if reconciled.WorkspaceRevision != current.WorkspaceRevision+1 {
		t.Fatalf("ref recovery revision = %d, want %d", reconciled.WorkspaceRevision, current.WorkspaceRevision+1)
	}
}

func TestGitRemoteSyncPushRejectionAndForceLease(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot,
		AttachRoots: []string{attachRoot}, AllowFileClone: true})
	if err != nil {
		t.Fatal(err)
	}
	remote, root := filepath.Join(t.TempDir(), "remote.git"), filepath.Join(attachRoot, "git-remote")
	runGit(t, "init", "--bare", remote)
	writeIntelligenceFile(t, root, "app.txt", "one\n")
	runGit(t, "init", "--initial-branch=main", root)
	runGit(t, "-C", root, "add", ".")
	runGitWithIdentity(t, "-C", root, "commit", "-m", "one")
	runGit(t, "-C", root, "remote", "add", "origin", "file://"+remote)
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "git-remote-attach"), AttachInput{Name: "Git remote", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := service.IssueRepositoryGrant(ctx, actor, RepositoryGrantRequest{ProjectID: project.ID,
		Provider: "local", Repository: "local", Actions: []string{"read", "push", "force-push"}, TTLSeconds: 300}, true)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := exactGitHead(ctx, root)
	push, err := service.PushGitRemote(ctx, actor, GitPushRequest{ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, ExpectedHead: head, Provider: "local", Remote: "origin",
		SourceRevision: head, TargetBranch: "main", PermissionGrant: grant, IdempotencyKey: "push-one"}, false, true)
	if err != nil || push.AfterHead != head || push.Classification != PolicyRed {
		t.Fatalf("initial push = %+v, %v", push, err)
	}
	repeated, err := service.PushGitRemote(ctx, actor, GitPushRequest{ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, ExpectedHead: head, Provider: "local", Remote: "origin",
		SourceRevision: head, TargetBranch: "main", PermissionGrant: grant, IdempotencyKey: "push-one"}, false, true)
	if err != nil || repeated.AfterHead != push.AfterHead {
		t.Fatalf("idempotent push = %+v, %v", repeated, err)
	}
	current, _ := service.Get(ctx, actor, project.ID)
	if _, err := service.SyncGitRemote(ctx, actor, GitSyncRequest{ProjectID: project.ID,
		WorkspaceRevision: current.WorkspaceRevision, ExpectedHead: head, Provider: "local", Remote: "origin",
		PermissionGrant: grant}); err != nil {
		t.Fatal(err)
	}

	other := filepath.Join(t.TempDir(), "other")
	runGit(t, "clone", "file://"+remote, other)
	runGit(t, "-C", other, "checkout", "main")
	if err := os.WriteFile(filepath.Join(other, "app.txt"), []byte("remote advance\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", other, "add", ".")
	runGitWithIdentity(t, "-C", other, "commit", "-m", "remote advance")
	runGit(t, "-C", other, "push", "origin", "main")
	remoteHeadBytes, _, _ := runGitBounded(ctx, other, "rev-parse", "HEAD")
	remoteHead := strings.TrimSpace(string(remoteHeadBytes))

	current, _ = service.Get(ctx, actor, project.ID)
	if _, err := service.PushGitRemote(ctx, actor, GitPushRequest{ProjectID: project.ID,
		WorkspaceRevision: current.WorkspaceRevision, ExpectedHead: head, Provider: "local", Remote: "origin",
		SourceRevision: head, TargetBranch: "main", PermissionGrant: grant, IdempotencyKey: "rejected"}, false, true); err == nil {
		t.Fatal("rejected non-fast-forward push succeeded")
	}
	if _, err := service.PushGitRemote(ctx, actor, GitPushRequest{ProjectID: project.ID,
		WorkspaceRevision: current.WorkspaceRevision, ExpectedHead: head, Provider: "local", Remote: "origin",
		SourceRevision: head, TargetBranch: "main", ExpectedRemoteHead: strings.Repeat("0", 40),
		PermissionGrant: grant, IdempotencyKey: "stale-lease"}, true, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale force lease = %v", err)
	}
	forced, err := service.PushGitRemote(ctx, actor, GitPushRequest{ProjectID: project.ID,
		WorkspaceRevision: current.WorkspaceRevision, ExpectedHead: head, Provider: "local", Remote: "origin",
		SourceRevision: head, TargetBranch: "main", ExpectedRemoteHead: remoteHead,
		PermissionGrant: grant, IdempotencyKey: "exact-lease"}, true, true)
	if err != nil || forced.BeforeHead != remoteHead || forced.AfterHead != head {
		t.Fatalf("force-with-lease = %+v, %v", forced, err)
	}
}

func TestGitHistoricalPreviewNeverChecksOutOverActiveDirtyTree(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(attachRoot, "git-preview")
	writeIntelligenceFile(t, root, "app.txt", "historical\n")
	runGit(t, "init", "--initial-branch=main", root)
	runGit(t, "-C", root, "add", ".")
	runGitWithIdentity(t, "-C", root, "commit", "-m", "historical")
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("active dirty work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "git-preview-attach"), AttachInput{Name: "Git preview", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.StartGitPreview(ctx, actor, GitPreviewRequest{ProjectID: project.ID, Revision: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.State != "active" || preview.OriginalBranch != "main" || !pathWithin(workspaceRoot, preview.Path) {
		t.Fatalf("historical preview = %+v", preview)
	}
	if content, err := os.ReadFile(filepath.Join(preview.Path, "app.txt")); err != nil || string(content) != "historical\n" {
		t.Fatalf("historical preview content = %q, %v", content, err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "app.txt")); err != nil || string(content) != "active dirty work\n" {
		t.Fatalf("active dirty work was changed = %q, %v", content, err)
	}
	if err := os.WriteFile(filepath.Join(preview.Path, "app.txt"), []byte("preview takeover\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CloseGitPreview(ctx, actor, GitPreviewCloseRequest{ProjectID: project.ID, PreviewID: preview.ID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("dirty preview close = %v", err)
	}
	if err := os.WriteFile(filepath.Join(preview.Path, "app.txt"), []byte("historical\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	closed, err := restarted.CloseGitPreview(ctx, actor, GitPreviewCloseRequest{ProjectID: project.ID, PreviewID: preview.ID})
	if err != nil || closed.State != "closed" {
		t.Fatalf("restart preview close = %+v, %v", closed, err)
	}
	if _, err := os.Lstat(preview.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview path survived close: %v", err)
	}
	if content, _ := os.ReadFile(filepath.Join(root, "app.txt")); string(content) != "active dirty work\n" {
		t.Fatalf("active dirty work changed after preview close: %q", content)
	}
}

func TestGitTypedMutationsScopeCommitAndPreserveUnrelatedStaging(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(attachRoot, "git-mutations")
	writeIntelligenceFile(t, root, "selected.txt", "selected-before\n")
	writeIntelligenceFile(t, root, "unrelated.txt", "unrelated-before\n")
	runGit(t, "init", "--initial-branch=main", root)
	runGit(t, "-C", root, "add", ".")
	runGitWithIdentity(t, "-C", root, "commit", "-m", "baseline")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "git-mutation-attach"), AttachInput{Name: "Git mutations", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := service.GitProjection(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("selected-after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("unrelated-staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", root, "add", "unrelated.txt")
	commitRequest := GitCommitRequest{ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		ExpectedHead: projection.Head, Message: "change selected only", AuthorName: "Ion Test",
		AuthorEmail: "ion@example.invalid", Paths: []GitPathExpectation{{Path: "selected.txt", SHA256: testContentHash("selected-after\n")}}}
	if _, err := service.CommitGitPaths(ctx, actor, commitRequest, false); err == nil {
		t.Fatal("unapproved commit was accepted")
	}
	receipt, err := service.CommitGitPaths(ctx, actor, commitRequest, true)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.BeforeHead == receipt.AfterHead || receipt.WorkspaceRevision != project.WorkspaceRevision+1 || receipt.Classification != PolicyRed {
		t.Fatalf("commit receipt = %+v", receipt)
	}
	status, _, err := runGitBounded(ctx, root, "status", "--porcelain=v1")
	if err != nil || !strings.Contains(string(status), "M  unrelated.txt") || strings.Contains(string(status), "selected.txt") {
		t.Fatalf("exact commit status = %q, %v", status, err)
	}
	current, err := service.Get(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	branchReceipt, err := service.CreateGitBranch(ctx, actor, GitBranchCreateRequest{ProjectID: project.ID,
		WorkspaceRevision: current.WorkspaceRevision, Name: "review/exact-scope", ExpectedHead: receipt.AfterHead})
	if err != nil || branchReceipt.Operation != "branch.create" {
		t.Fatalf("branch receipt = %+v, %v", branchReceipt, err)
	}
	current, _ = service.Get(ctx, actor, project.ID)
	tagReceipt, err := service.CreateGitTag(ctx, actor, GitTagRequest{ProjectID: project.ID,
		WorkspaceRevision: current.WorkspaceRevision, ExpectedHead: receipt.AfterHead, Name: "v0.0.1-test",
		Message: "test checkpoint", AuthorName: "Ion Test", AuthorEmail: "ion@example.invalid"}, true)
	if err != nil || tagReceipt.Operation != "tag.create" {
		t.Fatalf("tag receipt = %+v, %v", tagReceipt, err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep-untracked.txt"), []byte("do not restore over me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	current, _ = service.Get(ctx, actor, project.ID)
	restore, err := service.PlanGitRestore(ctx, actor, GitRestorePlanRequest{ProjectID: project.ID,
		WorkspaceRevision: current.WorkspaceRevision, Revision: receipt.BeforeHead, Paths: []string{"selected.txt"}})
	if err != nil || len(restore.Members) != 1 || restore.Members[0].Path != "selected.txt" {
		t.Fatalf("reviewable restore plan = %+v, %v", restore, err)
	}
	if _, err := service.ApplyPatchSetApproved(ctx, actor, restore, true); err != nil {
		t.Fatal(err)
	}
	if selected, _ := os.ReadFile(filepath.Join(root, "selected.txt")); string(selected) != "selected-before\n" {
		t.Fatalf("historical restore selected bytes = %q", selected)
	}
	if unrelated, _ := os.ReadFile(filepath.Join(root, "unrelated.txt")); string(unrelated) != "unrelated-staged\n" {
		t.Fatalf("historical restore altered unrelated staging = %q", unrelated)
	}
	if untracked, _ := os.ReadFile(filepath.Join(root, "keep-untracked.txt")); string(untracked) != "do not restore over me\n" {
		t.Fatalf("historical restore altered untracked work = %q", untracked)
	}
}
