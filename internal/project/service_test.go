package project

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type recordingEmitter struct {
	mu     sync.Mutex
	events []LifecycleEvent
}

func (emitter *recordingEmitter) EmitProjectEvent(_ context.Context, event LifecycleEvent) error {
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	emitter.events = append(emitter.events, event)
	return nil
}

func (emitter *recordingEmitter) snapshot() []LifecycleEvent {
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	return append([]LifecycleEvent(nil), emitter.events...)
}

func TestRegistryCreateImportAttachCloneRestartIsolationAndTeardown(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot := t.TempDir(), t.TempDir()
	importRoot, attachRoot := t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	clock := types.SystemClock{}
	service, err := NewService(store, clock, ServiceConfig{
		WorkspaceRoot: workspaceRoot, ImportRoots: []string{importRoot}, AttachRoots: []string{attachRoot},
		AllowFileClone: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	emitter := &recordingEmitter{}
	service.SetEmitter(emitter)
	actor, other := uuid.New(), uuid.New()

	createMeta := testMeta(actor, "create-template")
	created, err := service.CreateTemplate(ctx, createMeta, TemplateInput{
		Name: "Useful CLI", Template: "go-cli", Host: HostDirectLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Lifecycle != LifecycleReady || created.WorkspaceRevision != 2 ||
		len(created.StackSignals) != 1 || created.StackSignals[0] != "go" || !created.Managed {
		t.Fatalf("created project = %+v", created)
	}
	if info, err := os.Stat(filepath.Join(created.Root, "go.mod")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("curated template was not materialized: %v", err)
	}
	repeated, err := service.CreateTemplate(ctx, createMeta, TemplateInput{
		Name: "Useful CLI", Template: "go-cli", Host: HostDirectLocal,
	})
	if err != nil || repeated.ID != created.ID {
		t.Fatalf("idempotent create = %+v, %v", repeated, err)
	}
	if _, err := service.CreateTemplate(ctx, createMeta, TemplateInput{
		Name: "Different", Template: "empty", Host: HostDirectLocal,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting idempotency reuse = %v", err)
	}
	if projects, _, err := service.List(ctx, other); err != nil || len(projects) != 0 {
		t.Fatalf("cross-actor list = %+v, %v", projects, err)
	}
	if _, err := service.Get(ctx, other, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-actor get = %v", err)
	}

	attachedRoot := filepath.Join(attachRoot, "existing")
	if err := os.Mkdir(attachedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attachedRoot, "package.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	attached, err := service.AttachDirectory(ctx, testMeta(actor, "attach"), AttachInput{
		Name: "Existing app", Directory: attachedRoot,
	})
	if err != nil || attached.Managed || len(attached.StackSignals) != 1 || attached.StackSignals[0] != "node" {
		t.Fatalf("attached project = %+v, %v", attached, err)
	}
	if _, err := service.AttachDirectory(ctx, testMeta(other, "attach-other"), AttachInput{
		Name: "Leak", Directory: attachedRoot,
	}); err == nil {
		t.Fatal("cross-actor directory claim was accepted")
	}

	archivePath := filepath.Join(importRoot, "project.zip")
	writeZip(t, archivePath, map[string]string{"src/main.py": "print('ok')\n", "pyproject.toml": "[project]\nname='ok'\n"})
	imported, err := service.ImportArchive(ctx, testMeta(actor, "import"), ArchiveInput{
		Name: "Imported", ArchivePath: archivePath, Host: HostDirectLocal,
	})
	if err != nil || imported.Source != SourceArchive || len(imported.StackSignals) != 1 || imported.StackSignals[0] != "python" {
		t.Fatalf("imported project = %+v, %v", imported, err)
	}
	badArchive := filepath.Join(importRoot, "traversal.zip")
	writeZip(t, badArchive, map[string]string{"../escape": "denied"})
	if _, err := service.ImportArchive(ctx, testMeta(actor, "bad-import"), ArchiveInput{
		Name: "Bad import", ArchivePath: badArchive, Host: HostDirectLocal,
	}); err == nil {
		t.Fatal("archive traversal was accepted")
	}

	repository := filepath.Join(importRoot, "source")
	runGit(t, "init", "--initial-branch=main", repository)
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("# source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", repository, "add", "README.md")
	runGitWithIdentity(t, "-C", repository, "commit", "-m", "initial")
	cloned, err := service.CloneRepository(ctx, testMeta(actor, "clone"), CloneInput{
		Name: "Clone", RepositoryURL: "file://" + repository, Authorized: true,
		DefaultBranch: "main", Host: HostDirectLocal,
	})
	if err != nil || cloned.Source != SourceRepository || cloned.DefaultBranch != "main" {
		t.Fatalf("cloned project = %+v, %v", cloned, err)
	}
	if _, err := service.CloneRepository(ctx, testMeta(actor, "clone-denied"), CloneInput{
		Name: "Denied", RepositoryURL: "file://" + repository, Host: HostDirectLocal,
	}); err == nil {
		t.Fatal("unauthorized clone was accepted")
	}

	events := emitter.snapshot()
	if len(events) < 12 || events[0].State != "queued" || events[1].State != "started" ||
		events[2].State != "progress" || events[3].State != "completed" {
		t.Fatalf("lifecycle events = %+v", events)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	manager, store = reopenProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	restarted, err := NewService(store, clock, ServiceConfig{
		WorkspaceRoot: workspaceRoot, ImportRoots: []string{importRoot}, AttachRoots: []string{attachRoot},
		AllowFileClone: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	projects, _, err := restarted.List(ctx, actor)
	if err != nil || len(projects) != 5 {
		t.Fatalf("restart projects = %d, %v", len(projects), err)
	}
	stale := created.WorkspaceRevision - 1
	if _, err := restarted.Lifecycle(ctx, withRevision(testMeta(actor, "stale"), stale), LifecycleInput{
		ProjectID: created.ID, Operation: HostPause,
	}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale lifecycle = %v", err)
	}
	archived, err := restarted.Lifecycle(ctx, withRevision(testMeta(actor, "archive-project"), created.WorkspaceRevision), LifecycleInput{
		ProjectID: created.ID, Operation: HostArchive,
	})
	if err != nil || archived.LatestArchive == "" {
		t.Fatalf("archive lifecycle = %+v, %v", archived, err)
	}
	if _, err := os.Stat(archived.LatestArchive); err != nil {
		t.Fatalf("portable archive is missing: %v", err)
	}
	if _, err := restarted.Lifecycle(ctx, withRevision(testMeta(actor, "destroy-no-decision"), archived.WorkspaceRevision), LifecycleInput{
		ProjectID: created.ID, Operation: HostDestroy,
	}); err == nil {
		t.Fatal("destructive teardown without exact decision was accepted")
	}
	destroyed, err := restarted.Lifecycle(ctx, withRevision(testMeta(actor, "destroy-preserve"), archived.WorkspaceRevision), LifecycleInput{
		ProjectID: created.ID, Operation: HostDestroy, UncommittedWorkDecision: "preserve",
	})
	if err != nil || destroyed.ID != created.ID {
		t.Fatalf("preserving destroy = %+v, %v", destroyed, err)
	}
	if _, err := os.Stat(created.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed root survived destroy: %v", err)
	}
	if _, err := restarted.Get(ctx, actor, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("destroyed project remains registered: %v", err)
	}
	if _, err := restarted.Lifecycle(ctx, withRevision(testMeta(actor, "detach-delete"), attached.WorkspaceRevision), LifecycleInput{
		ProjectID: attached.ID, Operation: HostDestroy, UncommittedWorkDecision: "waive",
	}); err == nil {
		t.Fatal("attached directory deletion was accepted")
	}
}

func TestRegistryRejectsSymlinkEscapeAndResourceExhaustion(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot := t.TempDir(), t.TempDir()
	allowed, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(allowed, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	limited, err := NewLocalHost(LocalHostConfig{WorkspaceRoot: workspaceRoot,
		Limits: ResourceLimits{DiskBytes: 8}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{allowed}, LocalHost: limited,
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err := service.AttachDirectory(ctx, testMeta(actor, "escape"), AttachInput{
		Name: "Escape", Directory: link,
	}); err == nil {
		t.Fatal("symlink escape was accepted")
	}
	if _, err := service.CreateTemplate(ctx, testMeta(actor, "disk-limit"), TemplateInput{
		Name: "Too large", Template: "go-cli", Host: HostDirectLocal,
	}); err == nil {
		t.Fatal("disk resource exhaustion was accepted")
	}
	projects, _, err := service.List(ctx, actor)
	if err != nil || len(projects) != 1 || projects[0].Lifecycle != LifecycleFailed {
		t.Fatalf("failed resource operation was not durably reconcilable: %+v, %v", projects, err)
	}
}

func TestHostRejectsCrossProjectEnvelopeAndUnbrokeredEgress(t *testing.T) {
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	local, err := NewLocalHost(LocalHostConfig{WorkspaceRoot: workspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	project := Project{ID: uuid.New(), Root: filepath.Join(workspaceRoot, uuid.NewString()),
		WorkspaceRevision: 1, Managed: true, Lifecycle: LifecycleProvisioning}
	meta := testMeta(uuid.New(), "cross-project")
	runCtx, cancel := context.WithDeadline(ctx, meta.Deadline)
	defer cancel()
	envelope := OperationEnvelope{Version: WorkspaceHostVersion, Operation: HostProvision,
		ActorID: meta.ActorID, ProjectID: uuid.New(), WorkspaceRevision: project.WorkspaceRevision,
		RequestID: meta.RequestID, IdempotencyKey: meta.IdempotencyKey,
		PolicyClassification: meta.PolicyClassification, Deadline: meta.Deadline,
		CorrelationID: meta.CorrelationID, Payload: json.RawMessage(`{}`)}
	if _, err := local.Execute(runCtx, project, envelope); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("cross-project envelope = %v", err)
	}

	container, err := NewContainerHost(ContainerHostConfig{WorkspaceRoot: workspaceRoot,
		Image: "open-design-local:latest", Network: NetworkPolicy{Mode: "deny", AllowedHosts: []string{"example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	project.ID = envelope.ProjectID
	if _, err := container.Execute(runCtx, project, envelope); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unbrokered egress = %v", err)
	}
}

func TestContainerHostProductionLifecycle(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	if err := exec.Command("docker", "image", "inspect", "open-design-local:latest").Run(); err != nil {
		t.Skip("local non-root acceptance image is unavailable")
	}
	ctx := context.Background()
	dataRoot, workspaceRoot, importRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	container, err := NewContainerHost(ContainerHostConfig{WorkspaceRoot: workspaceRoot,
		Image: "open-design-local:latest", Network: NetworkPolicy{Mode: "deny"},
		Entrypoint: "/bin/sleep", Command: []string{"300"},
		Limits: ResourceLimits{WallTimeSecond: 120, OutputBytes: 256 << 10}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, ImportRoots: []string{importRoot},
		AllowFileClone: true, ContainerHost: container,
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	created, err := service.CreateTemplate(ctx, testMeta(actor, "container-create"), TemplateInput{
		Name: "Container workspace", Template: "empty", Host: HostContainer,
	})
	if err != nil {
		t.Fatal(err)
	}
	containerReferences := []string{created.HostReference}
	t.Cleanup(func() {
		for _, reference := range containerReferences {
			if reference != "" {
				_ = exec.Command("docker", "rm", "--force", reference).Run()
			}
		}
	})
	if created.Lifecycle != LifecycleReady || created.HostReference == "" {
		t.Fatalf("container project = %+v", created)
	}
	inspect, err := exec.Command("docker", "inspect", "--format",
		"{{.Config.User}}|{{.HostConfig.NetworkMode}}|{{.HostConfig.ReadonlyRootfs}}|{{.HostConfig.PidsLimit}}|{{.HostConfig.Memory}}|{{json .HostConfig.CapDrop}}|{{json .HostConfig.SecurityOpt}}|{{.HostConfig.LogConfig.Type}}|{{index .HostConfig.LogConfig.Config \"max-size\"}}|{{index .HostConfig.LogConfig.Config \"max-file\"}}",
		created.HostReference).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "65532:65532|none|true|128|536870912|[\"ALL\"]|[\"no-new-privileges\"]|local|262144|1\n"
	if string(inspect) != want {
		t.Fatalf("container isolation = %q, want %q", inspect, want)
	}
	paused, err := service.Lifecycle(ctx, withRevision(testMeta(actor, "container-pause"), created.WorkspaceRevision), LifecycleInput{
		ProjectID: created.ID, Operation: HostPause,
	})
	if err != nil || paused.Lifecycle != LifecyclePaused {
		t.Fatalf("pause = %+v, %v", paused, err)
	}
	resumed, err := service.Lifecycle(ctx, withRevision(testMeta(actor, "container-resume"), paused.WorkspaceRevision), LifecycleInput{
		ProjectID: created.ID, Operation: HostResume,
	})
	if err != nil || resumed.Lifecycle != LifecycleReady {
		t.Fatalf("resume = %+v, %v", resumed, err)
	}
	if err := service.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}

	// Reconstruct the host and service as a daemon restart would, then reconcile
	// the still-running container from encrypted registry state.
	restartedHost, err := NewContainerHost(ContainerHostConfig{WorkspaceRoot: workspaceRoot,
		Image: "open-design-local:latest", Network: NetworkPolicy{Mode: "deny"},
		Entrypoint: "/bin/sleep", Command: []string{"300"},
		Limits: ResourceLimits{WallTimeSecond: 120, OutputBytes: 256 << 10}})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, ImportRoots: []string{importRoot},
		AllowFileClone: true, ContainerHost: restartedHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	reconciled, err := restarted.Get(ctx, actor, created.ID)
	if err != nil || reconciled.Lifecycle != LifecycleReady || reconciled.HostReference != created.HostReference {
		t.Fatalf("container restart reconciliation = %+v, %v", reconciled, err)
	}

	archivePath := filepath.Join(importRoot, "container-import.zip")
	writeZip(t, archivePath, map[string]string{"package.json": "{}\n"})
	imported, err := restarted.ImportArchive(ctx, testMeta(actor, "container-import"), ArchiveInput{
		Name: "Container import", ArchivePath: archivePath, Host: HostContainer,
	})
	if err != nil || imported.Lifecycle != LifecycleReady {
		t.Fatalf("container import = %+v, %v", imported, err)
	}
	containerReferences = append(containerReferences, imported.HostReference)
	repository := filepath.Join(importRoot, "container-source")
	runGit(t, "init", "--initial-branch=main", repository)
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("# container source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", repository, "add", "README.md")
	runGitWithIdentity(t, "-C", repository, "commit", "-m", "initial")
	cloned, err := restarted.CloneRepository(ctx, testMeta(actor, "container-clone"), CloneInput{
		Name: "Container clone", RepositoryURL: "file://" + repository,
		DefaultBranch: "main", Authorized: true, Host: HostContainer,
	})
	if err != nil || cloned.Lifecycle != LifecycleReady || cloned.DefaultBranch != "main" {
		t.Fatalf("container clone = %+v, %v", cloned, err)
	}
	containerReferences = append(containerReferences, cloned.HostReference)

	archived, err := restarted.Lifecycle(ctx, withRevision(testMeta(actor, "container-archive"), reconciled.WorkspaceRevision), LifecycleInput{
		ProjectID: created.ID, Operation: HostArchive,
	})
	if err != nil || archived.LatestArchive == "" {
		t.Fatalf("container archive = %+v, %v", archived, err)
	}
	destroyed, err := restarted.Lifecycle(ctx, withRevision(testMeta(actor, "container-destroy"), archived.WorkspaceRevision), LifecycleInput{
		ProjectID: created.ID, Operation: HostDestroy, UncommittedWorkDecision: "preserve",
	})
	if err != nil || destroyed.ID != created.ID {
		t.Fatalf("container destroy = %+v, %v", destroyed, err)
	}
	for _, project := range []Project{imported, cloned} {
		if _, err := restarted.Lifecycle(ctx, withRevision(testMeta(actor, "container-cleanup-"+project.ID.String()), project.WorkspaceRevision), LifecycleInput{
			ProjectID: project.ID, Operation: HostDestroy, UncommittedWorkDecision: "waive",
		}); err != nil {
			t.Fatalf("container cleanup %s: %v", project.Name, err)
		}
	}
}

func TestContainerHostStopsWorkspaceOnDiskExhaustion(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	if err := exec.Command("docker", "image", "inspect", "open-design-local:latest").Run(); err != nil {
		t.Skip("local non-root acceptance image is unavailable")
	}
	ctx := context.Background()
	dataRoot, workspaceRoot := t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	host, err := NewContainerHost(ContainerHostConfig{WorkspaceRoot: workspaceRoot,
		Image: "open-design-local:latest", Network: NetworkPolicy{Mode: "deny"},
		Entrypoint: "/bin/sleep", Command: []string{"300"},
		Limits: ResourceLimits{DiskBytes: 4096, WallTimeSecond: 120, OutputBytes: 64 << 10}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, ContainerHost: host,
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	created, err := service.CreateTemplate(ctx, testMeta(actor, "bounded-container"), TemplateInput{
		Name: "Bounded container", Template: "empty", Host: HostContainer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", created.HostReference).Run() })
	if err := os.WriteFile(filepath.Join(created.Root, "flood.bin"), make([]byte, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		output, inspectErr := exec.Command("docker", "inspect", "--format", "{{.State.Status}}", created.HostReference).Output()
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		if string(output) == "exited\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("container was not stopped after disk limit exhaustion")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := service.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	reconciled, err := service.Get(ctx, actor, created.ID)
	if err != nil || reconciled.Lifecycle != LifecycleStopped {
		t.Fatalf("resource-stop reconciliation = %+v, %v", reconciled, err)
	}
	if _, err := service.Lifecycle(ctx, withRevision(testMeta(actor, "bounded-container-destroy"), reconciled.WorkspaceRevision), LifecycleInput{
		ProjectID: created.ID, Operation: HostDestroy, UncommittedWorkDecision: "waive",
	}); err != nil {
		t.Fatal(err)
	}
}

func testMeta(actor uuid.UUID, key string) OperationMeta {
	return OperationMeta{ActorID: actor, RequestID: uuid.New(), IdempotencyKey: key,
		PolicyClassification: PolicyYellow, Deadline: time.Now().Add(2 * time.Minute),
		CorrelationID: uuid.New()}
}

func withRevision(meta OperationMeta, revision uint64) OperationMeta {
	meta.ExpectedRevision = &revision
	return meta
}

func openProjectStore(t testing.TB, ctx context.Context, root string) (*vault.Manager, *session.Store) {
	t.Helper()
	source, err := vault.NewFileKEKSource(filepath.Join(root, "development.kek"))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := vault.NewFileWrappedKeyStore(filepath.Join(root, "user-key.enc"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := vault.Initialize(ctx, source, keys)
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(ctx, filepath.Join(root, "sessions.db"), manager.Vault(), types.SystemClock{}, 128*1024)
	if err != nil {
		_ = manager.Close()
		t.Fatal(err)
	}
	return manager, store
}

func reopenProjectStore(t testing.TB, ctx context.Context, root string) (*vault.Manager, *session.Store) {
	t.Helper()
	source, err := vault.NewFileKEKSource(filepath.Join(root, "development.kek"))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := vault.NewFileWrappedKeyStore(filepath.Join(root, "user-key.enc"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := vault.Open(ctx, source, keys)
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(ctx, filepath.Join(root, "sessions.db"), manager.Vault(), types.SystemClock{}, 128*1024)
	if err != nil {
		_ = manager.Close()
		t.Fatal(err)
	}
	return manager, store
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range files {
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1"}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

func runGitWithIdentity(t *testing.T, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Ion Acceptance", "GIT_AUTHOR_EMAIL=acceptance@example.invalid",
		"GIT_COMMITTER_NAME=Ion Acceptance", "GIT_COMMITTER_EMAIL=acceptance@example.invalid"}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

func TestOperationEnvelopeRejectsExpiredAndSecretBearingGrant(t *testing.T) {
	now := time.Now()
	envelope := OperationEnvelope{Version: WorkspaceHostVersion, Operation: HostReadiness,
		ActorID: uuid.New(), ProjectID: uuid.New(), WorkspaceRevision: 1,
		RequestID: uuid.New(), IdempotencyKey: "one", PolicyClassification: PolicyGreen,
		Deadline: now.Add(time.Minute), CorrelationID: uuid.New(), Payload: json.RawMessage(`{}`)}
	if err := envelope.Validate(now); err != nil {
		t.Fatal(err)
	}
	envelope.SecretGrants = []SecretGrant{{Reference: "vault://project/token", ExpiresAt: now.Add(2 * time.Minute)}}
	if err := envelope.Validate(now); err == nil {
		t.Fatal("grant outliving operation deadline was accepted")
	}
}

func TestVaultIssuedSecretGrantRestartAndActorIsolation(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot := t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	grant, err := service.IssueSecretGrant(ctx, actor, "vault://repositories/example", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if grant.ID == uuid.Nil || grant.Token == "" {
		t.Fatalf("issued grant is incomplete: %+v", grant)
	}
	if err := service.verifyGrant(ctx, actor, grant); err != nil {
		t.Fatalf("issued grant verification: %v", err)
	}
	if err := service.verifyGrant(ctx, uuid.New(), grant); err == nil {
		t.Fatal("cross-actor grant was accepted")
	}
	tampered := grant
	tampered.Reference = "vault://repositories/other"
	if err := service.verifyGrant(ctx, actor, tampered); err == nil {
		t.Fatal("tampered grant was accepted")
	}
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := os.ReadFile(filepath.Join(dataRoot, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(database, []byte(grant.Reference)) || bytes.Contains(database, []byte(grant.Token)) {
		t.Fatal("grant authority or reference leaked from encrypted storage")
	}
	manager, store = reopenProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	restarted, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.verifyGrant(ctx, actor, grant); err != nil {
		t.Fatalf("restart grant verification: %v", err)
	}
}
