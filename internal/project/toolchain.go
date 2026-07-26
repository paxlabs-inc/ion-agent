package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type RuntimeDiscovery struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Available bool   `json:"available"`
}

type ToolchainReport struct {
	ProjectID         uuid.UUID          `json:"project_id"`
	WorkspaceRevision uint64             `json:"workspace_revision"`
	Runtimes          []RuntimeDiscovery `json:"runtimes"`
	Lockfiles         []string           `json:"lockfiles"`
	PackageManager    string             `json:"package_manager,omitempty"`
	BuildSystems      []string           `json:"build_systems"`
	LifecycleScripts  []string           `json:"lifecycle_scripts"`
	RequiredVersions  map[string]string  `json:"required_versions"`
}

type DependencyRequest struct {
	ProjectID         uuid.UUID `json:"project_id"`
	WorkspaceRevision uint64    `json:"workspace_revision"`
	WorkingDirectory  string    `json:"working_directory,omitempty"`
	Manager           string    `json:"manager,omitempty"`
	Packages          []string  `json:"packages,omitempty"`
	AllowScripts      bool      `json:"allow_scripts,omitempty"`
	NetworkAllowed    bool      `json:"network_allowed"`
}

type DependencyPlan struct {
	ProjectID          uuid.UUID            `json:"project_id"`
	WorkspaceRevision  uint64               `json:"workspace_revision"`
	WorkingDirectory   string               `json:"working_directory"`
	Manager            string               `json:"manager"`
	Lockfile           string               `json:"lockfile"`
	LockfileSHA256     string               `json:"lockfile_sha256"`
	Argv               []string             `json:"argv"`
	Packages           []string             `json:"packages"`
	LifecycleScripts   []string             `json:"lifecycle_scripts"`
	Risks              []string             `json:"risks"`
	Classification     PolicyClassification `json:"classification"`
	RequiresApproval   bool                 `json:"requires_approval"`
	ScriptsWillExecute bool                 `json:"scripts_will_execute"`
}

func (service *Service) DiscoverToolchain(ctx context.Context, actor, projectID uuid.UUID) (ToolchainReport, error) {
	project, err := service.Get(ctx, actor, projectID)
	if err != nil {
		return ToolchainReport{}, err
	}
	return discoverToolchain(ctx, project, project.Root)
}

func discoverToolchain(ctx context.Context, project Project, root string) (ToolchainReport, error) {
	report := ToolchainReport{ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Runtimes: []RuntimeDiscovery{}, Lockfiles: []string{}, BuildSystems: []string{},
		LifecycleScripts: []string{}, RequiredVersions: map[string]string{}}
	for _, runtime := range []struct{ name, command string }{{"Go", "go"}, {"Node.js", "node"}, {"npm", "npm"},
		{"pnpm", "pnpm"}, {"Yarn", "yarn"}, {"Python", "python3"}, {"Rust", "rustc"}, {"Cargo", "cargo"}} {
		found := RuntimeDiscovery{Name: runtime.name}
		if path, lookupErr := exec.LookPath(runtime.command); lookupErr == nil {
			found.Path, found.Available = path, true
			versionCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			output, _ := exec.CommandContext(versionCtx, path, "--version").CombinedOutput()
			cancel()
			redacted, _ := redactSecrets("toolchain-version", output)
			found.Version = strings.TrimSpace(string(redacted))
			if len(found.Version) > 256 {
				found.Version = found.Version[:256]
			}
		}
		report.Runtimes = append(report.Runtimes, found)
	}
	for _, candidate := range []struct{ file, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"},
		{"package-lock.json", "npm"}, {"Cargo.lock", "cargo"}, {"go.sum", "go"}, {"poetry.lock", "poetry"}, {"uv.lock", "uv"}} {
		if regularProjectFile(root, candidate.file) {
			report.Lockfiles = append(report.Lockfiles, candidate.file)
			if report.PackageManager == "" {
				report.PackageManager = candidate.manager
			}
		}
	}
	for _, candidate := range []struct{ file, system string }{{"go.mod", "Go modules"}, {"package.json", "Node.js"},
		{"Cargo.toml", "Cargo"}, {"pyproject.toml", "Python packaging"}, {"Makefile", "Make"}} {
		if regularProjectFile(root, candidate.file) {
			report.BuildSystems = append(report.BuildSystems, candidate.system)
		}
	}
	readPackagePolicy(root, &report)
	return report, nil
}

func (service *Service) PlanDependencies(ctx context.Context, actor uuid.UUID, request DependencyRequest) (DependencyPlan, error) {
	project, err := service.Get(ctx, actor, request.ProjectID)
	if err != nil {
		return DependencyPlan{}, err
	}
	if project.WorkspaceRevision != request.WorkspaceRevision {
		return DependencyPlan{}, ErrStaleRevision
	}
	directory, err := secureWorkspaceDirectory(project.Root, request.WorkingDirectory)
	if err != nil {
		return DependencyPlan{}, err
	}
	report, err := discoverToolchain(ctx, project, directory)
	if err != nil {
		return DependencyPlan{}, err
	}
	manager, lockfile := report.PackageManager, ""
	if request.Manager != "" {
		manager = strings.ToLower(strings.TrimSpace(request.Manager))
		for _, candidate := range []struct{ file, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"},
			{"package-lock.json", "npm"}, {"Cargo.lock", "cargo"}, {"go.sum", "go"}, {"poetry.lock", "poetry"}, {"uv.lock", "uv"}} {
			if candidate.manager == manager && regularProjectFile(directory, candidate.file) {
				lockfile = candidate.file
			}
		}
	} else if len(report.Lockfiles) == 1 {
		lockfile = report.Lockfiles[0]
	}
	if manager == "" || lockfile == "" {
		return DependencyPlan{}, fmt.Errorf("project: exactly one supported lockfile is required")
	}
	digest, err := digestRegularProjectFile(directory, lockfile)
	if err != nil {
		return DependencyPlan{}, err
	}
	packages := append([]string(nil), request.Packages...)
	sort.Strings(packages)
	plan := DependencyPlan{ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		WorkingDirectory: cleanRelativePath(request.WorkingDirectory), Manager: manager, Lockfile: lockfile, LockfileSHA256: digest,
		Packages: packages, LifecycleScripts: report.LifecycleScripts,
		Classification: PolicyYellow, ScriptsWillExecute: request.AllowScripts}
	for _, item := range packages {
		if strings.ContainsAny(item, " \t\r\n") || strings.Contains(item, "://") || strings.HasPrefix(item, "git+") ||
			strings.HasPrefix(item, "file:") || strings.HasSuffix(item, "@latest") || !strings.Contains(item, "@") {
			plan.Risks = append(plan.Risks, "untrusted or unpinned package reference: "+item)
		}
	}
	if len(report.LifecycleScripts) > 0 {
		plan.Risks = append(plan.Risks, "repository declares package lifecycle scripts")
	}
	plan.Argv, err = dependencyArgv(plan.Manager, packages, request.AllowScripts)
	if err != nil {
		return DependencyPlan{}, err
	}
	if request.AllowScripts || len(plan.Risks) > 0 {
		plan.Classification, plan.RequiresApproval = PolicyRed, true
	}
	return plan, nil
}

func (service *Service) InstallDependencies(ctx context.Context, actor uuid.UUID, request DependencyRequest,
	approved bool) (TerminalState, DependencyPlan, error) {
	plan, err := service.PlanDependencies(ctx, actor, request)
	if err != nil {
		return TerminalState{}, DependencyPlan{}, err
	}
	if !request.NetworkAllowed {
		return TerminalState{}, plan, fmt.Errorf("project: dependency installation requires explicit network authority")
	}
	if plan.RequiresApproval && !approved {
		return TerminalState{}, plan, fmt.Errorf("project: dependency plan requires approval")
	}
	state, err := service.StartProcess(ctx, actor, ProcessRequest{ProjectID: request.ProjectID,
		WorkspaceRevision: request.WorkspaceRevision, Mode: ProcessOneShot, Argv: plan.Argv,
		WorkingDirectory: request.WorkingDirectory, TimeoutSeconds: 1800, OutputBytes: 2 << 20})
	return state, plan, err
}

func dependencyArgv(manager string, packages []string, allowScripts bool) ([]string, error) {
	switch manager {
	case "npm":
		if len(packages) > 0 {
			argv := []string{"npm", "install", "--save-exact"}
			if !allowScripts {
				argv = append(argv, "--ignore-scripts")
			}
			return append(argv, packages...), nil
		}
		argv := []string{"npm", "ci"}
		if !allowScripts {
			argv = append(argv, "--ignore-scripts")
		}
		return argv, nil
	case "pnpm":
		if len(packages) > 0 {
			argv := []string{"pnpm", "add", "--save-exact"}
			if !allowScripts {
				argv = append(argv, "--ignore-scripts")
			}
			return append(argv, packages...), nil
		}
		argv := []string{"pnpm", "install", "--frozen-lockfile"}
		if !allowScripts {
			argv = append(argv, "--ignore-scripts")
		}
		return argv, nil
	case "yarn":
		if len(packages) > 0 {
			argv := append([]string{"yarn", "add", "--exact"}, packages...)
			if !allowScripts {
				argv = append(argv, "--mode=skip-builds")
			}
			return argv, nil
		}
		argv := []string{"yarn", "install", "--immutable"}
		if !allowScripts {
			argv = append(argv, "--mode=skip-builds")
		}
		return argv, nil
	case "go":
		if len(packages) > 0 {
			return append([]string{"go", "get"}, packages...), nil
		}
		return []string{"go", "mod", "download"}, nil
	case "cargo":
		if len(packages) > 0 {
			return nil, fmt.Errorf("project: Cargo additions require a reviewed manifest patch before fetch")
		}
		return []string{"cargo", "fetch", "--locked"}, nil
	case "poetry":
		if len(packages) > 0 {
			return append([]string{"poetry", "add"}, packages...), nil
		}
		return []string{"poetry", "install", "--sync"}, nil
	case "uv":
		if len(packages) > 0 {
			return append([]string{"uv", "add"}, packages...), nil
		}
		return []string{"uv", "sync", "--frozen"}, nil
	default:
		return nil, ErrUnsupported
	}
}

func readPackagePolicy(root string, report *ToolchainReport) {
	path, err := securePatchPath(root, "package.json", false)
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 2<<20 {
		return
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
		Engines map[string]string `json:"engines"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return
	}
	for _, name := range []string{"preinstall", "install", "postinstall", "prepare"} {
		if strings.TrimSpace(manifest.Scripts[name]) != "" {
			report.LifecycleScripts = append(report.LifecycleScripts, name)
		}
	}
	for name, version := range manifest.Engines {
		report.RequiredVersions[name] = version
	}
}

func regularProjectFile(root, relative string) bool {
	path, err := securePatchPath(root, relative, false)
	if err != nil {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

func digestRegularProjectFile(root, relative string) (string, error) {
	path, err := securePatchPath(root, relative, false)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
