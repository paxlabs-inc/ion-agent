package project

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
)

const gitRemoteDeliveryKind = "project_git_remote_delivery_v1"

// GitCredentialBroker resolves a previously granted write-only credential
// reference directly into a process environment. Implementations must return
// a cleanup callback and must never place secret values in arguments or URLs.
type GitCredentialBroker interface {
	PrepareGitCredential(context.Context, string, string, string) ([]string, func(), error)
}

// GitNetworkBroker pins and authorizes the validated HTTPS destination used by
// the Git child process. Direct HTTPS remotes are refused without it because
// Git's own resolver cannot enforce the application's DNS-rebinding policy.
type GitNetworkBroker interface {
	AuthorizeGitRemote(context.Context, string) error
}

type gitRemoteDelivery struct {
	RequestHash string           `json:"request_hash"`
	Receipt     GitRemoteReceipt `json:"receipt"`
}

func (service *Service) SyncGitRemote(ctx context.Context, actor uuid.UUID,
	request GitSyncRequest) (GitRemoteReceipt, error) {
	return service.applyGitRemote(ctx, actor, request.ProjectID, request.WorkspaceRevision,
		request.ExpectedHead, request.Provider, request.Remote, request.CredentialReference,
		request.SecretGrant, request.PermissionGrant, "read", "sync", PolicyYellow, func(root, remote string, env []string) (GitRemoteReceipt, error) {
			_, _, err := runGitRemoteBounded(ctx, root, env, "fetch", "--prune", "--no-tags", "--", remote)
			return GitRemoteReceipt{}, err
		})
}

func (service *Service) MergeGitRevision(ctx context.Context, actor uuid.UUID,
	request GitMergeRequest, authorized bool) (GitRemoteReceipt, error) {
	if !authorized || !validGitRefInput(strings.TrimSpace(request.Revision)) {
		return GitRemoteReceipt{}, fmt.Errorf("project: exact merge requires explicit approval")
	}
	service.gitMu.Lock()
	defer service.gitMu.Unlock()
	project, root, err := service.requireGitState(ctx, actor, request.ProjectID, request.WorkspaceRevision, request.ExpectedHead, true)
	if err != nil {
		return GitRemoteReceipt{}, err
	}
	before := request.ExpectedHead
	arguments := []string{"merge", "--no-edit", "--no-verify"}
	if message := strings.TrimSpace(request.Message); message != "" {
		arguments = append(arguments, "-m", message)
	}
	arguments = append(arguments, "--", strings.TrimSpace(request.Revision))
	if _, _, err := runGitMutableBounded(ctx, root, arguments...); err != nil {
		_, _, _ = runGitMutableBounded(context.Background(), root, "merge", "--abort")
		return GitRemoteReceipt{}, fmt.Errorf("project: merge stopped; conflicts were aborted: %w", err)
	}
	after, err := exactGitHead(ctx, root)
	if err != nil {
		return GitRemoteReceipt{}, err
	}
	updated, err := service.bumpWorkspaceRevision(ctx, actor, request.ProjectID, project.WorkspaceRevision)
	if err != nil {
		return GitRemoteReceipt{}, err
	}
	return GitRemoteReceipt{Version: GitContractVersion, ProjectID: request.ProjectID, Operation: "merge",
		BeforeHead: before, AfterHead: after, WorkspaceRevision: updated.WorkspaceRevision,
		Classification: PolicyRed, AppliedAt: service.clock.Now().UTC()}, nil
}

func (service *Service) PullGitRemote(ctx context.Context, actor uuid.UUID,
	request GitPullRequest, authorized bool) (GitRemoteReceipt, error) {
	if !authorized || !validGitRefInput(strings.TrimSpace(request.Branch)) {
		return GitRemoteReceipt{}, fmt.Errorf("project: exact pull requires explicit approval")
	}
	return service.applyGitRemote(ctx, actor, request.ProjectID, request.WorkspaceRevision,
		request.ExpectedHead, request.Provider, request.Remote, request.CredentialReference,
		request.SecretGrant, request.PermissionGrant, "read", "pull", PolicyRed, func(root, remote string, env []string) (GitRemoteReceipt, error) {
			if err := requireCleanGit(ctx, root); err != nil {
				return GitRemoteReceipt{}, err
			}
			if _, _, err := runGitRemoteBounded(ctx, root, env, "fetch", "--no-tags", "--", remote, request.Branch); err != nil {
				return GitRemoteReceipt{}, err
			}
			if _, _, err := runGitMutableBounded(ctx, root, "merge", "--no-edit", "--no-verify", "FETCH_HEAD"); err != nil {
				_, _, _ = runGitMutableBounded(context.Background(), root, "merge", "--abort")
				return GitRemoteReceipt{}, fmt.Errorf("project: pull stopped; conflicts were aborted: %w", err)
			}
			return GitRemoteReceipt{Target: request.Branch}, nil
		})
}

func (service *Service) PushGitRemote(ctx context.Context, actor uuid.UUID,
	request GitPushRequest, force, authorized bool) (GitRemoteReceipt, error) {
	operation, action := "push", "push"
	if force {
		operation, action = "force-with-lease", "force-push"
	}
	if !authorized || strings.TrimSpace(request.IdempotencyKey) == "" || len(request.IdempotencyKey) > 256 ||
		!validGitRefInput(strings.TrimSpace(request.SourceRevision)) || !validGitRefInput(strings.TrimSpace(request.TargetBranch)) ||
		(force && strings.TrimSpace(request.ExpectedRemoteHead) == "") {
		return GitRemoteReceipt{}, fmt.Errorf("project: exact approved %s request is required", operation)
	}
	encoded, _ := json.Marshal(request)
	digest := sha256.Sum256(append(encoded, byte(operation[0])))
	requestHash := hex.EncodeToString(digest[:])
	scope := actor.String() + ":" + request.ProjectID.String() + ":" + operation + ":" + request.IdempotencyKey
	if raw, err := service.store.LoadLivingState(ctx, gitRemoteDeliveryKind, scope); err == nil {
		var delivery gitRemoteDelivery
		if json.Unmarshal(raw, &delivery) != nil || delivery.RequestHash != requestHash {
			return GitRemoteReceipt{}, ErrConflict
		}
		return delivery.Receipt, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return GitRemoteReceipt{}, err
	}
	receipt, err := service.applyGitRemote(ctx, actor, request.ProjectID, request.WorkspaceRevision,
		request.ExpectedHead, request.Provider, request.Remote, request.CredentialReference,
		request.SecretGrant, request.PermissionGrant, action, operation, PolicyRed, func(root, remote string, env []string) (GitRemoteReceipt, error) {
			target := "refs/heads/" + strings.TrimPrefix(strings.TrimSpace(request.TargetBranch), "refs/heads/")
			local, _, err := runGitBounded(ctx, root, "rev-parse", "--verify", request.SourceRevision+"^{}")
			if err != nil {
				return GitRemoteReceipt{}, err
			}
			localHead := strings.TrimSpace(string(local))
			before, err := gitRemoteHead(ctx, root, env, remote, target)
			if err != nil {
				return GitRemoteReceipt{}, err
			}
			if force && before != strings.TrimSpace(request.ExpectedRemoteHead) {
				return GitRemoteReceipt{}, fmt.Errorf("%w: remote branch changed before force-with-lease", ErrConflict)
			}
			args := []string{"push", "--porcelain"}
			if force {
				args = append(args, "--force-with-lease="+target+":"+request.ExpectedRemoteHead)
			}
			args = append(args, "--", remote, localHead+":"+target)
			_, _, pushErr := runGitRemoteBounded(ctx, root, env, args...)
			after, inspectErr := gitRemoteHead(context.Background(), root, env, remote, target)
			if pushErr != nil && (inspectErr != nil || after != localHead) {
				return GitRemoteReceipt{}, fmt.Errorf("project: %s outcome is uncertain; retry with the same idempotency key: %w", operation, pushErr)
			}
			return GitRemoteReceipt{Target: target, BeforeHead: before, AfterHead: after,
				Reconciled: pushErr != nil && after == localHead}, nil
		})
	if err != nil {
		return GitRemoteReceipt{}, err
	}
	raw, _ := json.Marshal(gitRemoteDelivery{RequestHash: requestHash, Receipt: receipt})
	if err := service.store.SaveLivingState(ctx, gitRemoteDeliveryKind, scope, raw); err != nil {
		return GitRemoteReceipt{}, err
	}
	return receipt, nil
}

func (service *Service) applyGitRemote(ctx context.Context, actor, projectID uuid.UUID,
	workspaceRevision uint64, expectedHead, provider, remote, credentialReference string,
	credentialGrant SecretGrant, permissionGrant, action, operation string, classification PolicyClassification,
	apply func(string, string, []string) (GitRemoteReceipt, error)) (GitRemoteReceipt, error) {
	service.gitMu.Lock()
	defer service.gitMu.Unlock()
	project, root, err := service.requireGitState(ctx, actor, projectID, workspaceRevision, expectedHead, false)
	if err != nil {
		return GitRemoteReceipt{}, err
	}
	remote = strings.TrimSpace(remote)
	if !validGitRemoteName(remote) {
		return GitRemoteReceipt{}, fmt.Errorf("project: valid configured remote name is required")
	}
	remoteURLRaw, _, err := runGitBounded(ctx, root, "remote", "get-url", "--push", remote)
	if err != nil {
		return GitRemoteReceipt{}, fmt.Errorf("project: remote %q is missing: %w", remote, err)
	}
	remoteURL, err := service.validateRepositoryURL(strings.TrimSpace(string(remoteURLRaw)))
	if err != nil {
		return GitRemoteReceipt{}, err
	}
	if parsed, _ := url.Parse(remoteURL); parsed != nil && parsed.Scheme == "https" {
		broker, ok := service.gitCredentialBroker.(GitNetworkBroker)
		if !ok {
			return GitRemoteReceipt{}, fmt.Errorf("project: HTTPS Git remote requires the SSRF-safe integration broker")
		}
		if err := broker.AuthorizeGitRemote(ctx, remoteURL); err != nil {
			return GitRemoteReceipt{}, err
		}
	}
	repository := repositoryScopeFromURL(remoteURL)
	if err := service.verifyRepositoryGrant(ctx, actor, projectID, provider, repository, action, permissionGrant); err != nil {
		return GitRemoteReceipt{}, err
	}
	if err := service.verifyProviderCredential(ctx, actor, credentialReference, credentialGrant); err != nil {
		return GitRemoteReceipt{}, err
	}
	env, cleanup, err := service.gitCredentialEnvironment(ctx, credentialReference, permissionGrant, remoteURL)
	if err != nil {
		return GitRemoteReceipt{}, err
	}
	defer cleanup()
	partial, err := apply(root, remote, env)
	if err != nil {
		return GitRemoteReceipt{}, err
	}
	after, err := exactGitHead(ctx, root)
	if err != nil {
		return GitRemoteReceipt{}, err
	}
	updated, err := service.bumpWorkspaceRevision(ctx, actor, projectID, project.WorkspaceRevision)
	if err != nil {
		return GitRemoteReceipt{}, err
	}
	partial.Version, partial.ProjectID, partial.Operation, partial.Remote = GitContractVersion, projectID, operation, remote
	if partial.BeforeHead == "" {
		partial.BeforeHead = expectedHead
	}
	if partial.AfterHead == "" {
		partial.AfterHead = after
	}
	partial.WorkspaceRevision, partial.Classification = updated.WorkspaceRevision, classification
	partial.AppliedAt = service.clock.Now().UTC()
	return partial, nil
}

func (service *Service) requireGitState(ctx context.Context, actor, projectID uuid.UUID,
	workspaceRevision uint64, expectedHead string, clean bool) (Project, string, error) {
	project, root, err := service.gitProject(ctx, actor, projectID)
	if err != nil {
		return Project{}, "", err
	}
	if project.WorkspaceRevision != workspaceRevision {
		return Project{}, "", ErrStaleRevision
	}
	head, err := exactGitHead(ctx, root)
	if err != nil || head != strings.TrimSpace(expectedHead) {
		return Project{}, "", errors.Join(ErrConflict, err)
	}
	if clean {
		err = requireCleanGit(ctx, root)
	}
	return project, root, err
}

func requireCleanGit(ctx context.Context, root string) error {
	status, _, err := runGitBounded(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if len(status) != 0 {
		return fmt.Errorf("project: merge and pull require a clean working tree")
	}
	return nil
}

func exactGitHead(ctx context.Context, root string) (string, error) {
	head, _, err := runGitBounded(ctx, root, "rev-parse", "--verify", "HEAD")
	return strings.TrimSpace(string(head)), err
}

func validGitRemoteName(remote string) bool {
	return validGitRefInput(remote) && !strings.ContainsAny(remote, "/\\: ") && remote != "." && remote != ".."
}

func repositoryScopeFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "file" {
		return "local"
	}
	return strings.TrimSuffix(strings.TrimPrefix(parsed.Path, "/"), ".git")
}

func (service *Service) gitCredentialEnvironment(ctx context.Context, reference, grant, remoteURL string) ([]string, func(), error) {
	if strings.TrimSpace(reference) == "" {
		return nil, func() {}, nil
	}
	if service.gitCredentialBroker == nil {
		return nil, nil, fmt.Errorf("project: credential broker is unavailable")
	}
	env, cleanup, err := service.gitCredentialBroker.PrepareGitCredential(ctx, reference, grant, remoteURL)
	if cleanup == nil {
		cleanup = func() {}
	}
	return env, cleanup, err
}

func gitRemoteHead(ctx context.Context, root string, env []string, remote, target string) (string, error) {
	output, _, err := runGitRemoteBounded(ctx, root, env, "ls-remote", "--heads", "--", remote, target)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return "", nil
	}
	if len(fields) != 2 || fields[1] != target {
		return "", fmt.Errorf("project: unexpected remote reference response")
	}
	return fields[0], nil
}

func runGitRemoteBounded(ctx context.Context, root string, extraEnv []string, arguments ...string) ([]byte, bool, error) {
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	argv := append([]string{"-c", "safe.directory=" + root, "-C", root}, arguments...)
	command := exec.CommandContext(runCtx, "git", argv...)
	command.Env = append([]string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/nonexistent", "LANG=C.UTF-8",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=1", "GIT_TERMINAL_PROMPT=0"}, extraEnv...)
	var output boundedGitBuffer
	command.Stdout, command.Stderr = &output, &output
	err := command.Run()
	if runCtx.Err() != nil {
		return output.Bytes(), output.truncated, runCtx.Err()
	}
	if err != nil {
		return output.Bytes(), output.truncated, fmt.Errorf("git %s: %w: %s", arguments[0], err,
			strings.TrimSpace(redactGitOutput(output.Bytes())))
	}
	return output.Bytes(), output.truncated, nil
}
