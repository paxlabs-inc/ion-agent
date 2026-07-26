package project

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const projectMarkerName = ".ion-project"

var defaultLimits = ResourceLimits{
	CPUMillis: 1000, MemoryBytes: 512 << 20, Processes: 128,
	DiskBytes: 2 << 30, WallTimeSecond: 1800, OutputBytes: 1 << 20,
}

// LocalHostConfig bounds the wider-authority direct-local implementation.
type LocalHostConfig struct {
	WorkspaceRoot string
	ArchiveRoot   string
	Limits        ResourceLimits
}

// LocalHost operates only marked managed roots (or read-only attached roots)
// and discloses that it shares the daemon's OS authority.
type LocalHost struct {
	workspaceRoot string
	archiveRoot   string
	limits        ResourceLimits
}

func NewLocalHost(config LocalHostConfig) (*LocalHost, error) {
	root, archives, err := hostRoots(config.WorkspaceRoot, config.ArchiveRoot)
	if err != nil {
		return nil, err
	}
	limits := normalizeLimits(config.Limits)
	return &LocalHost{workspaceRoot: root, archiveRoot: archives, limits: limits}, nil
}

func (host *LocalHost) Capabilities(context.Context) HostCapabilities {
	return HostCapabilities{
		Version: WorkspaceHostVersion, Kind: HostDirectLocal, Available: true,
		Domains: capabilities(map[CapabilityDomain][]string{
			CapabilityProject: {"provision", "readiness", "pause", "resume", "stop", "archive", "destroy"},
			CapabilityFile:    {"root-confined-access"}, CapabilityGit: {"repository-discovery"},
			CapabilityArtifact: {"portable-tar-gzip"}, CapabilitySystem: {"resource-preflight"},
		}),
		Limits: limitsCopy(host.limits), Network: NetworkPolicy{Mode: "policy_dispatcher"},
		RootConfined: true, NonRoot: os.Geteuid() != 0,
		AuthorityDisclosure: "Direct-local workspaces share the Ion daemon's operating-system authority; path, policy, revision, and audit checks still apply.",
	}
}

func (host *LocalHost) Execute(ctx context.Context, project Project, envelope OperationEnvelope) (HostResult, error) {
	if err := validateHostCall(ctx, project, envelope); err != nil {
		return HostResult{}, err
	}
	if err := host.validateRoot(project); err != nil {
		return HostResult{}, err
	}
	switch envelope.Operation {
	case HostProvision:
		if !project.Managed {
			return HostResult{}, fmt.Errorf("%w: attached roots cannot be provisioned", ErrConflict)
		}
		if err := os.MkdirAll(project.Root, 0o700); err != nil {
			return HostResult{}, fmt.Errorf("project: provision local root: %w", err)
		}
		if err := writeProjectMarker(project); err != nil {
			return HostResult{}, err
		}
		if err := enforceDiskLimit(project.Root, host.limits.DiskBytes); err != nil {
			return HostResult{}, err
		}
		return HostResult{State: LifecycleReady, Message: "local workspace ready"}, nil
	case HostReadiness:
		if err := validateExistingRoot(project.Root); err != nil {
			return HostResult{}, err
		}
		if err := enforceDiskLimit(project.Root, host.limits.DiskBytes); err != nil {
			return HostResult{}, err
		}
		return HostResult{State: project.Lifecycle, Message: "local workspace reachable"}, nil
	case HostPause:
		return HostResult{State: LifecyclePaused, Message: "new local operations paused"}, nil
	case HostResume:
		if err := validateExistingRoot(project.Root); err != nil {
			return HostResult{}, err
		}
		return HostResult{State: LifecycleReady, Message: "local operations resumed"}, nil
	case HostStop:
		return HostResult{State: LifecycleStopped, Message: "local workspace stopped"}, nil
	case HostArchive:
		archive, err := archiveProject(ctx, project, host.archiveRoot, host.limits)
		if err != nil {
			return HostResult{}, err
		}
		return HostResult{State: LifecycleArchived, HostReference: archive, Message: "workspace archived"}, nil
	case HostDestroy:
		return host.destroy(ctx, project, envelope)
	default:
		return HostResult{}, ErrUnsupported
	}
}

func (host *LocalHost) Reconcile(ctx context.Context, project Project) (HostResult, error) {
	if err := ctx.Err(); err != nil {
		return HostResult{}, err
	}
	if err := host.validateRoot(project); err != nil {
		return HostResult{}, err
	}
	if err := validateExistingRoot(project.Root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return HostResult{State: LifecycleFailed, Message: "workspace root is missing"}, nil
		}
		return HostResult{}, err
	}
	if err := enforceDiskLimit(project.Root, host.limits.DiskBytes); err != nil {
		return HostResult{State: LifecycleFailed, Message: err.Error()}, nil
	}
	return HostResult{State: project.Lifecycle, HostReference: project.HostReference, Message: "local workspace reconciled"}, nil
}

func (host *LocalHost) validateRoot(project Project) error {
	if project.Managed {
		if !pathWithin(host.workspaceRoot, project.Root) {
			return fmt.Errorf("project: managed root escapes workspace root")
		}
		return nil
	}
	return validateExistingRoot(project.Root)
}

func (host *LocalHost) destroy(ctx context.Context, project Project, envelope OperationEnvelope) (HostResult, error) {
	if !project.Managed {
		return HostResult{}, fmt.Errorf("project: attached directories are detached, never deleted")
	}
	decision, err := teardownDecision(envelope.Payload)
	if err != nil {
		return HostResult{}, err
	}
	if decision == "preserve" {
		if _, err := archiveProject(ctx, project, host.archiveRoot, host.limits); err != nil {
			return HostResult{}, err
		}
	}
	if err := validateProjectMarker(project); err != nil {
		return HostResult{}, err
	}
	if err := os.RemoveAll(project.Root); err != nil {
		return HostResult{}, fmt.Errorf("project: destroy managed root: %w", err)
	}
	return HostResult{State: LifecycleStopped, Message: "managed workspace destroyed after exact decision"}, nil
}

// ContainerHostConfig configures a non-root, deny-egress Docker worker. The
// daemon may itself be rootful; the workspace process is always non-root and
// stripped of Linux capabilities.
type ContainerHostConfig struct {
	WorkspaceRoot string
	ArchiveRoot   string
	Runtime       string
	Image         string
	User          string
	Limits        ResourceLimits
	Network       NetworkPolicy
	Entrypoint    string
	Command       []string
}

type ContainerHost struct {
	workspaceRoot string
	archiveRoot   string
	runtime       string
	image         string
	user          string
	limits        ResourceLimits
	network       NetworkPolicy
	entrypoint    string
	commandArgs   []string
	mu            sync.Mutex
	monitors      map[string]context.CancelFunc
}

func NewContainerHost(config ContainerHostConfig) (*ContainerHost, error) {
	root, archives, err := hostRoots(config.WorkspaceRoot, config.ArchiveRoot)
	if err != nil {
		return nil, err
	}
	runtime := strings.TrimSpace(config.Runtime)
	if runtime == "" {
		runtime = "docker"
	}
	if strings.ContainsAny(runtime, `/\\`) {
		return nil, fmt.Errorf("project: container runtime must be an executable name")
	}
	user := strings.TrimSpace(config.User)
	if user == "" {
		if os.Geteuid() == 0 {
			user = "65532:65532"
		} else {
			user = strconv.Itoa(os.Geteuid()) + ":" + strconv.Itoa(os.Getegid())
		}
	}
	network := config.Network
	if network.Mode == "" {
		network.Mode = "deny"
	}
	return &ContainerHost{workspaceRoot: root, archiveRoot: archives,
		runtime: runtime, image: strings.TrimSpace(config.Image), user: user,
		limits: normalizeLimits(config.Limits), network: network,
		entrypoint: strings.TrimSpace(config.Entrypoint), commandArgs: append([]string(nil), config.Command...),
		monitors: make(map[string]context.CancelFunc)}, nil
}

func (host *ContainerHost) Capabilities(ctx context.Context) HostCapabilities {
	reason := ""
	available := host.image != ""
	if !available {
		reason = "no container image is configured"
	} else if _, err := exec.LookPath(host.runtime); err != nil {
		available, reason = false, "container runtime is not installed"
	} else {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if _, err := host.command(probe, "version", "--format", "{{.Server.Version}}"); err != nil {
			available, reason = false, "container runtime is unavailable"
		}
	}
	return HostCapabilities{
		Version: WorkspaceHostVersion, Kind: HostContainer, Available: available, Reason: reason,
		Domains: capabilities(map[CapabilityDomain][]string{
			CapabilityProject: {"provision", "readiness", "pause", "resume", "stop", "archive", "destroy"},
			CapabilityFile:    {"root-mounted-workspace"}, CapabilityProcess: {"non-root-supervision"},
			CapabilityGit: {"workspace-repository"}, CapabilityPreview: {"scoped-port-declaration"},
			CapabilityArtifact: {"portable-tar-gzip"}, CapabilitySystem: {"cpu", "memory", "pids", "wall-time", "output", "disk-preflight"},
		}),
		Limits: limitsCopy(host.limits), Network: host.network,
		NonRoot: true, RootConfined: true,
	}
}

func (host *ContainerHost) Execute(ctx context.Context, project Project, envelope OperationEnvelope) (HostResult, error) {
	if err := validateHostCall(ctx, project, envelope); err != nil {
		return HostResult{}, err
	}
	if !project.Managed || !pathWithin(host.workspaceRoot, project.Root) {
		return HostResult{}, fmt.Errorf("project: container root is not managed and confined")
	}
	if strings.Contains(project.Root, ",") {
		return HostResult{}, fmt.Errorf("project: container root contains unsupported delimiter")
	}
	if host.network.Mode != "deny" || len(host.network.AllowedHosts) != 0 {
		return HostResult{}, fmt.Errorf("%w: allowlisted egress requires a configured network broker", ErrUnsupported)
	}
	switch envelope.Operation {
	case HostProvision:
		return host.provision(ctx, project)
	case HostReadiness:
		return host.inspect(ctx, project)
	case HostPause:
		if _, err := host.command(ctx, "pause", project.HostReference); err != nil {
			return HostResult{}, err
		}
		return HostResult{State: LifecyclePaused, HostReference: project.HostReference}, nil
	case HostResume:
		if _, err := host.command(ctx, "unpause", project.HostReference); err != nil {
			return HostResult{}, err
		}
		return HostResult{State: LifecycleReady, HostReference: project.HostReference}, nil
	case HostStop:
		if _, err := host.command(ctx, "stop", "--time", "10", project.HostReference); err != nil {
			return HostResult{}, err
		}
		host.cancelMonitor(project.HostReference)
		return HostResult{State: LifecycleStopped, HostReference: project.HostReference}, nil
	case HostArchive:
		archive, err := archiveProject(ctx, project, host.archiveRoot, host.limits)
		if err != nil {
			return HostResult{}, err
		}
		return HostResult{State: LifecycleArchived, HostReference: archive}, nil
	case HostDestroy:
		return host.destroy(ctx, project, envelope)
	default:
		return HostResult{}, ErrUnsupported
	}
}

func (host *ContainerHost) Reconcile(ctx context.Context, project Project) (HostResult, error) {
	if project.HostReference == "" {
		return HostResult{State: LifecycleFailed, Message: "container reference is missing"}, nil
	}
	result, err := host.inspect(ctx, project)
	if err != nil {
		return HostResult{State: LifecycleFailed, Message: "container is missing or unavailable"}, nil
	}
	if result.State == LifecycleReady || result.State == LifecyclePaused {
		host.monitor(project.HostReference, project.Root)
	}
	return result, nil
}

func (host *ContainerHost) provision(ctx context.Context, project Project) (HostResult, error) {
	capabilities := host.Capabilities(ctx)
	if !capabilities.Available {
		return HostResult{}, fmt.Errorf("%w: %s", ErrUnsupported, capabilities.Reason)
	}
	if err := os.MkdirAll(project.Root, 0o700); err != nil {
		return HostResult{}, err
	}
	if err := writeProjectMarker(project); err != nil {
		return HostResult{}, err
	}
	if err := host.prepareOwnership(project.Root); err != nil {
		return HostResult{}, err
	}
	if err := enforceDiskLimit(project.Root, host.limits.DiskBytes); err != nil {
		return HostResult{}, err
	}
	name := "ion-" + strings.ReplaceAll(project.ID.String(), "-", "")
	args := []string{"create", "--name", name,
		"--label", "ion.project=" + project.ID.String(),
		"--user", host.user, "--workdir", "/workspace", "--network", "none",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--ulimit", "nofile=1024:1024",
		"--log-driver", "local", "--log-opt", "max-size=" + strconv.FormatInt(host.limits.OutputBytes, 10),
		"--log-opt", "max-file=1", "--log-opt", "compress=false",
		"--pids-limit", strconv.FormatInt(host.limits.Processes, 10),
		"--memory", strconv.FormatInt(host.limits.MemoryBytes, 10),
		"--cpus", fmt.Sprintf("%.3f", float64(host.limits.CPUMillis)/1000),
		"--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=67108864",
		"--mount", "type=bind,src=" + project.Root + ",dst=/workspace",
	}
	for _, port := range host.network.ExposedPorts {
		args = append(args, "--publish", "127.0.0.1::"+strconv.Itoa(int(port)))
	}
	if host.entrypoint != "" {
		args = append(args, "--entrypoint", host.entrypoint)
	}
	args = append(args, host.image)
	args = append(args, host.commandArgs...)
	id, err := host.command(ctx, args...)
	if err != nil {
		return HostResult{}, err
	}
	id = strings.TrimSpace(id)
	if _, err := host.command(ctx, "start", id); err != nil {
		_, _ = host.command(context.Background(), "rm", "--force", id)
		return HostResult{}, err
	}
	host.monitor(id, project.Root)
	return HostResult{State: LifecycleReady, HostReference: id, Message: "non-root container workspace ready"}, nil
}

func (host *ContainerHost) prepareOwnership(root string) error {
	parts := strings.Split(host.user, ":")
	if len(parts) != 2 {
		return fmt.Errorf("project: container user must be numeric uid:gid")
	}
	uid, uidErr := strconv.Atoi(parts[0])
	gid, gidErr := strconv.Atoi(parts[1])
	if uidErr != nil || gidErr != nil || uid <= 0 || gid <= 0 {
		return fmt.Errorf("project: container user must be non-root numeric uid:gid")
	}
	if os.Geteuid() != 0 {
		if uid != os.Geteuid() || gid != os.Getegid() {
			return fmt.Errorf("project: non-root daemon cannot delegate a different workspace uid")
		}
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("project: container workspaces reject symlinks")
		}
		if err := os.Chown(path, uid, gid); err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return nil
	})
}

func (host *ContainerHost) inspect(ctx context.Context, project Project) (HostResult, error) {
	if strings.TrimSpace(project.HostReference) == "" {
		return HostResult{}, fmt.Errorf("project: container reference is required")
	}
	state, err := host.command(ctx, "inspect", "--format", "{{.State.Status}}", project.HostReference)
	if err != nil {
		return HostResult{}, err
	}
	switch strings.TrimSpace(state) {
	case "running":
		return HostResult{State: LifecycleReady, HostReference: project.HostReference}, nil
	case "paused":
		return HostResult{State: LifecyclePaused, HostReference: project.HostReference}, nil
	case "created", "exited", "dead":
		host.cancelMonitor(project.HostReference)
		return HostResult{State: LifecycleStopped, HostReference: project.HostReference}, nil
	default:
		return HostResult{State: LifecycleFailed, HostReference: project.HostReference, Message: "unknown container state"}, nil
	}
}

func (host *ContainerHost) destroy(ctx context.Context, project Project, envelope OperationEnvelope) (HostResult, error) {
	decision, err := teardownDecision(envelope.Payload)
	if err != nil {
		return HostResult{}, err
	}
	if decision == "preserve" {
		if _, err := archiveProject(ctx, project, host.archiveRoot, host.limits); err != nil {
			return HostResult{}, err
		}
	}
	if project.HostReference != "" {
		host.cancelMonitor(project.HostReference)
		if _, err := host.command(ctx, "rm", "--force", project.HostReference); err != nil {
			return HostResult{}, err
		}
	}
	if err := validateProjectMarker(project); err != nil {
		return HostResult{}, err
	}
	if err := os.RemoveAll(project.Root); err != nil {
		return HostResult{}, err
	}
	return HostResult{State: LifecycleStopped, Message: "container and managed root destroyed after exact decision"}, nil
}

func (host *ContainerHost) monitor(reference, root string) {
	if strings.TrimSpace(reference) == "" {
		return
	}
	host.mu.Lock()
	if _, exists := host.monitors[reference]; exists {
		host.mu.Unlock()
		return
	}
	monitorCtx, cancel := context.WithCancel(context.Background())
	host.monitors[reference] = cancel
	host.mu.Unlock()
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		timer := time.NewTimer(time.Duration(host.limits.WallTimeSecond) * time.Second)
		defer ticker.Stop()
		defer timer.Stop()
		defer host.removeMonitor(reference)
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-timer.C:
				host.stopBounded(reference)
				return
			case <-ticker.C:
				if enforceDiskLimit(root, host.limits.DiskBytes) != nil {
					host.stopBounded(reference)
					return
				}
			}
		}
	}()
}

func (host *ContainerHost) stopBounded(reference string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = host.command(ctx, "stop", "--time", "5", reference)
}

func (host *ContainerHost) cancelMonitor(reference string) {
	host.mu.Lock()
	cancel := host.monitors[reference]
	delete(host.monitors, reference)
	host.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (host *ContainerHost) removeMonitor(reference string) {
	host.mu.Lock()
	delete(host.monitors, reference)
	host.mu.Unlock()
}

// Close stops local resource monitors without stopping durable containers;
// the next daemon instance reconciles and resumes monitoring them.
func (host *ContainerHost) Close() error {
	host.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(host.monitors))
	for _, cancel := range host.monitors {
		cancels = append(cancels, cancel)
	}
	host.monitors = make(map[string]context.CancelFunc)
	host.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return nil
}

func (host *ContainerHost) command(ctx context.Context, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, host.runtime, arguments...)
	var output strings.Builder
	bounded := &boundedWriter{writer: &output, remaining: host.limits.OutputBytes}
	command.Stdout = bounded
	command.Stderr = bounded
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("project: container runtime operation failed: %w: %s", err, strings.TrimSpace(output.String()))
	}
	return output.String(), nil
}

type boundedWriter struct {
	mu        sync.Mutex
	writer    io.Writer
	remaining int64
}

func (writer *boundedWriter) Write(payload []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if int64(len(payload)) > writer.remaining {
		return 0, fmt.Errorf("project: process output limit exceeded")
	}
	written, err := writer.writer.Write(payload)
	writer.remaining -= int64(written)
	return written, err
}

func validateHostCall(ctx context.Context, project Project, envelope OperationEnvelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := envelope.Validate(time.Now()); err != nil {
		return err
	}
	if envelope.ProjectID != project.ID {
		return ErrInvalidEnvelope
	}
	if envelope.WorkspaceRevision != project.WorkspaceRevision {
		return ErrStaleRevision
	}
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(envelope.Deadline) {
		return fmt.Errorf("%w: context must enforce envelope deadline", ErrInvalidEnvelope)
	}
	return nil
}

func hostRoots(workspaceRoot, archiveRoot string) (string, string, error) {
	root, err := secureRoot(workspaceRoot)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(archiveRoot) == "" {
		archiveRoot = filepath.Join(root, ".archives")
	}
	archives, err := secureRoot(archiveRoot)
	if err != nil {
		return "", "", err
	}
	return root, archives, nil
}

func secureRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("project: host root is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func normalizeLimits(limits ResourceLimits) ResourceLimits {
	if limits.CPUMillis <= 0 {
		limits.CPUMillis = defaultLimits.CPUMillis
	}
	if limits.MemoryBytes <= 0 {
		limits.MemoryBytes = defaultLimits.MemoryBytes
	}
	if limits.Processes <= 0 {
		limits.Processes = defaultLimits.Processes
	}
	if limits.DiskBytes <= 0 {
		limits.DiskBytes = defaultLimits.DiskBytes
	}
	if limits.WallTimeSecond <= 0 {
		limits.WallTimeSecond = defaultLimits.WallTimeSecond
	}
	if limits.OutputBytes <= 0 {
		limits.OutputBytes = defaultLimits.OutputBytes
	}
	return limits
}

func limitsCopy(limits ResourceLimits) ResourceLimits { return limits }

func capabilities(supported map[CapabilityDomain][]string) []Capability {
	result := make([]Capability, 0, len(capabilityDomains))
	for _, domain := range capabilityDomains {
		features, ok := supported[domain]
		capability := Capability{Domain: domain, Supported: ok, Features: append([]string(nil), features...)}
		if !ok {
			capability.Reason = "not negotiated by this host version"
		}
		result = append(result, capability)
	}
	return result
}

func pathWithin(root, candidate string) bool {
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, absolute)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validateExistingRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("project: workspace root must be a real directory")
	}
	return nil
}

func writeProjectMarker(project Project) error {
	payload, err := json.Marshal(map[string]string{"project_id": project.ID.String()})
	if err != nil {
		return err
	}
	marker := filepath.Join(project.Root, projectMarkerName)
	if existing, readErr := os.ReadFile(marker); readErr == nil {
		if string(existing) != string(payload) {
			return fmt.Errorf("project: workspace is owned by another project")
		}
		return nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func validateProjectMarker(project Project) error {
	payload, err := os.ReadFile(filepath.Join(project.Root, projectMarkerName))
	if err != nil {
		return fmt.Errorf("project: managed-root marker is unavailable: %w", err)
	}
	var marker struct {
		ProjectID string `json:"project_id"`
	}
	if json.Unmarshal(payload, &marker) != nil || marker.ProjectID != project.ID.String() {
		return fmt.Errorf("project: managed-root marker does not match")
	}
	return nil
}

func teardownDecision(payload json.RawMessage) (string, error) {
	var input struct {
		Decision string `json:"uncommitted_work_decision"`
	}
	if len(payload) == 0 || json.Unmarshal(payload, &input) != nil ||
		(input.Decision != "preserve" && input.Decision != "waive") {
		return "", fmt.Errorf("project: exact preserve or waive decision is required")
	}
	return input.Decision, nil
}

func enforceDiskLimit(root string, limit int64) error {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
			if total > limit {
				return fmt.Errorf("project: workspace disk limit exceeded")
			}
		}
		return nil
	})
	return err
}

func archiveProject(ctx context.Context, project Project, archiveRoot string, limits ResourceLimits) (string, error) {
	if err := validateExistingRoot(project.Root); err != nil {
		return "", err
	}
	if err := enforceDiskLimit(project.Root, limits.DiskBytes); err != nil {
		return "", err
	}
	final := filepath.Join(archiveRoot, project.ID.String()+"-r"+strconv.FormatUint(project.WorkspaceRevision, 10)+".tar.gz")
	temporary, err := os.CreateTemp(archiveRoot, ".project-archive-*.tmp")
	if err != nil {
		return "", err
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporaryName)
		}
	}()
	gzipWriter := gzip.NewWriter(temporary)
	tarWriter := tar.NewWriter(gzipWriter)
	err = filepath.WalkDir(project.Root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(project.Root, path)
		if err != nil || relative == "." {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("project: archive refuses symlinks")
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tarWriter, io.LimitReader(file, limits.DiskBytes+1))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err == nil {
		err = tarWriter.Close()
	} else {
		_ = tarWriter.Close()
	}
	if err == nil {
		err = gzipWriter.Close()
	} else {
		_ = gzipWriter.Close()
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err := os.Rename(temporaryName, final); err != nil {
		return "", err
	}
	committed = true
	return final, nil
}
