package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	gitBaselineKind = "project_git_baseline_v1"
	gitPreviewKind  = "project_git_preview_v1"
	maxGitOutput    = 2 << 20
)

func (service *Service) GitProjection(ctx context.Context, actor, projectID uuid.UUID) (GitProjection, error) {
	project, root, err := service.gitProject(ctx, actor, projectID)
	if err != nil {
		return GitProjection{}, err
	}
	statusRaw, truncated, err := runGitBounded(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return GitProjection{}, err
	}
	head, _, _ := runGitBounded(ctx, root, "rev-parse", "--verify", "HEAD")
	branch, _, branchErr := runGitBounded(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	projection := GitProjection{Version: GitContractVersion, ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, RepositoryRoot: root,
		Head: strings.TrimSpace(string(head)), Branch: strings.TrimSpace(string(branch)),
		Detached: branchErr != nil, Status: parseGitStatus(statusRaw), Truncated: truncated}
	projection.Branches, err = gitBranches(ctx, root)
	if err != nil {
		return GitProjection{}, err
	}
	projection.Remotes, err = gitRemotes(ctx, root)
	if err != nil {
		return GitProjection{}, err
	}
	projection.History, err = gitHistory(ctx, root, 64)
	if err != nil && projection.Head != "" {
		return GitProjection{}, err
	}
	staged, stagedTruncated, err := runGitBounded(ctx, root, "diff", "--cached", "--find-renames", "--binary", "--no-ext-diff")
	if err != nil {
		return GitProjection{}, err
	}
	unstaged, unstagedTruncated, err := runGitBounded(ctx, root, "diff", "--find-renames", "--binary", "--no-ext-diff")
	if err != nil {
		return GitProjection{}, err
	}
	projection.StagedDiff, projection.UnstagedDiff = redactGitOutput(staged), redactGitOutput(unstaged)
	projection.Truncated = projection.Truncated || stagedTruncated || unstagedTruncated
	projection.Baseline, err = service.gitBaseline(ctx, actor, project, root, statusRaw, projection.Head, projection.Branch)
	return projection, err
}

func (service *Service) GitBlame(ctx context.Context, actor uuid.UUID, request GitBlameRequest) ([]GitBlameLine, error) {
	_, root, err := service.gitProject(ctx, actor, request.ProjectID)
	if err != nil {
		return nil, err
	}
	path, err := securePatchPath(root, request.Path, false)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	if request.StartLine == 0 {
		request.StartLine = 1
	}
	if request.EndLine == 0 {
		request.EndLine = request.StartLine + 199
	}
	if request.StartLine < 1 || request.EndLine < request.StartLine || request.EndLine-request.StartLine > 999 {
		return nil, fmt.Errorf("project: bounded blame line range is required")
	}
	output, _, err := runGitBounded(ctx, root, "blame", "--line-porcelain",
		"-L", strconv.Itoa(request.StartLine)+","+strconv.Itoa(request.EndLine), "--", filepath.ToSlash(relative))
	if err != nil {
		return nil, err
	}
	return parseGitBlame(output, request.StartLine), nil
}

func (service *Service) GitUnstagedDiff(ctx context.Context, actor uuid.UUID, request GitDiffRequest) (GitDiffSelection, error) {
	_, root, err := service.gitProject(ctx, actor, request.ProjectID)
	if err != nil {
		return GitDiffSelection{}, err
	}
	if len(request.Paths) == 0 || len(request.Paths) > maxPatchMembers {
		return GitDiffSelection{}, fmt.Errorf("project: bounded Git diff paths are required")
	}
	arguments := []string{"diff", "--binary", "--no-ext-diff", "--"}
	for _, raw := range request.Paths {
		path := cleanRelativePath(raw)
		if path == "" {
			return GitDiffSelection{}, fmt.Errorf("project: valid Git diff path is required")
		}
		if _, err := securePatchPath(root, path, true); err != nil {
			return GitDiffSelection{}, err
		}
		arguments = append(arguments, path)
	}
	patch, truncated, err := runGitBounded(ctx, root, arguments...)
	if err != nil {
		return GitDiffSelection{}, err
	}
	head, _ := exactGitHead(ctx, root)
	return GitDiffSelection{ProjectID: request.ProjectID, Head: head, Patch: redactGitOutput(patch),
		SHA256: byteDigest(patch), Truncated: truncated}, nil
}

func (service *Service) gitProject(ctx context.Context, actor, projectID uuid.UUID) (Project, string, error) {
	project, err := service.Get(ctx, actor, projectID)
	if err != nil {
		return Project{}, "", err
	}
	rootOutput, _, err := runGitBounded(ctx, project.Root, "rev-parse", "--show-toplevel")
	if err != nil {
		return Project{}, "", fmt.Errorf("project: Git repository is unavailable: %w", err)
	}
	root := filepath.Clean(strings.TrimSpace(string(rootOutput)))
	if !pathWithin(project.Root, root) {
		return Project{}, "", ErrProtectedPath
	}
	return project, root, nil
}

func (service *Service) gitBaseline(ctx context.Context, actor uuid.UUID, project Project, root string,
	status []byte, head, branch string) (GitBaseline, error) {
	scope := patchScope(actor, project.ID)
	raw, err := service.store.LoadLivingState(ctx, gitBaselineKind, scope)
	if err == nil {
		var baseline GitBaseline
		if json.Unmarshal(raw, &baseline) != nil || baseline.ProjectID != project.ID || baseline.Version != GitContractVersion {
			return GitBaseline{}, fmt.Errorf("project: invalid encrypted Git baseline")
		}
		return baseline, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return GitBaseline{}, err
	}
	digest := sha256.Sum256(status)
	baseline := GitBaseline{Version: GitContractVersion, ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, RepositoryRoot: root, Head: head, Branch: branch,
		StatusSHA256: hex.EncodeToString(digest[:]), CapturedAt: service.clock.Now().UTC()}
	raw, err = json.Marshal(baseline)
	if err != nil {
		return GitBaseline{}, err
	}
	if err := service.store.SaveLivingState(ctx, gitBaselineKind, scope, raw); err != nil {
		return GitBaseline{}, err
	}
	return baseline, nil
}

func (service *Service) StartGitPreview(ctx context.Context, actor uuid.UUID, request GitPreviewRequest) (GitPreview, error) {
	project, root, err := service.gitProject(ctx, actor, request.ProjectID)
	if err != nil {
		return GitPreview{}, err
	}
	revision := strings.TrimSpace(request.Revision)
	if revision == "" || len(revision) > 200 || strings.ContainsAny(revision, "\x00\r\n") {
		return GitPreview{}, fmt.Errorf("project: bounded Git revision is required")
	}
	verified, _, err := runGitBounded(ctx, root, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return GitPreview{}, err
	}
	commit := strings.TrimSpace(string(verified))
	head, _, err := runGitBounded(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return GitPreview{}, err
	}
	branch, _, _ := runGitBounded(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	preview := GitPreview{Version: GitContractVersion, ID: uuid.New(), ActorID: actor,
		ProjectID: project.ID, RepositoryRoot: root, Revision: commit,
		OriginalHead: strings.TrimSpace(string(head)), OriginalBranch: strings.TrimSpace(string(branch)),
		State: "prepared", CreatedAt: service.clock.Now().UTC()}
	preview.Path = filepath.Join(service.workspaceRoot, ".git-previews", actor.String(), preview.ID.String())
	if !pathWithin(service.workspaceRoot, preview.Path) {
		return GitPreview{}, ErrProtectedPath
	}
	if err := service.saveGitPreview(ctx, preview); err != nil {
		return GitPreview{}, err
	}
	if err := os.MkdirAll(filepath.Dir(preview.Path), 0o700); err != nil {
		return GitPreview{}, err
	}
	if _, _, err := runGitMutableBounded(ctx, root, "worktree", "add", "--detach", preview.Path, commit); err != nil {
		return GitPreview{}, err
	}
	preview.State = "active"
	if err := service.saveGitPreview(ctx, preview); err != nil {
		return GitPreview{}, err
	}
	return preview, nil
}

func (service *Service) CloseGitPreview(ctx context.Context, actor uuid.UUID,
	request GitPreviewCloseRequest) (GitPreview, error) {
	preview, scope, err := service.loadGitPreview(ctx, actor, request.ProjectID, request.PreviewID)
	if err != nil {
		return GitPreview{}, err
	}
	status, _, statusErr := runGitBounded(ctx, preview.Path, "status", "--porcelain=v1", "--untracked-files=all")
	if statusErr != nil && !errors.Is(statusErr, os.ErrNotExist) {
		return GitPreview{}, statusErr
	}
	if len(bytes.TrimSpace(status)) > 0 {
		return GitPreview{}, fmt.Errorf("%w: historical preview contains uncommitted work", ErrConflict)
	}
	if _, _, err := runGitMutableBounded(ctx, preview.RepositoryRoot, "worktree", "remove", preview.Path); err != nil {
		return GitPreview{}, err
	}
	now := service.clock.Now().UTC()
	preview.State, preview.ClosedAt = "closed", &now
	if err := service.store.DeleteLivingState(ctx, gitPreviewKind, scope); err != nil {
		return GitPreview{}, err
	}
	return preview, nil
}

func (service *Service) saveGitPreview(ctx context.Context, preview GitPreview) error {
	raw, err := json.Marshal(preview)
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(ctx, gitPreviewKind, gitPreviewScope(preview.ActorID, preview.ProjectID, preview.ID), raw)
}

func (service *Service) loadGitPreview(ctx context.Context, actor, projectID, previewID uuid.UUID) (GitPreview, string, error) {
	if actor == uuid.Nil || projectID == uuid.Nil || previewID == uuid.Nil {
		return GitPreview{}, "", fmt.Errorf("project: complete Git preview identity is required")
	}
	scope := gitPreviewScope(actor, projectID, previewID)
	raw, err := service.store.LoadLivingState(ctx, gitPreviewKind, scope)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GitPreview{}, "", ErrNotFound
		}
		return GitPreview{}, "", err
	}
	var preview GitPreview
	if json.Unmarshal(raw, &preview) != nil || preview.ActorID != actor || preview.ProjectID != projectID ||
		preview.ID != previewID || preview.Version != GitContractVersion {
		return GitPreview{}, "", fmt.Errorf("project: invalid encrypted Git preview")
	}
	return preview, scope, nil
}

func (service *Service) reconcileGitPreviews(ctx context.Context) error {
	states, err := service.store.ListLivingStates(ctx, gitPreviewKind)
	if err != nil {
		return err
	}
	for _, state := range states {
		var preview GitPreview
		if json.Unmarshal(state.State, &preview) != nil || preview.Version != GitContractVersion ||
			!pathWithin(service.workspaceRoot, preview.Path) {
			return fmt.Errorf("project: invalid encrypted Git preview")
		}
		if preview.State != "prepared" {
			continue
		}
		if info, statErr := os.Lstat(preview.Path); statErr == nil && info.IsDir() {
			status, _, statusErr := runGitBounded(ctx, preview.Path, "status", "--porcelain=v1", "--untracked-files=all")
			if statusErr != nil || len(bytes.TrimSpace(status)) > 0 {
				return fmt.Errorf("%w: interrupted historical preview requires review", ErrConflict)
			}
			if _, _, removeErr := runGitMutableBounded(ctx, preview.RepositoryRoot, "worktree", "remove", preview.Path); removeErr != nil {
				return removeErr
			}
		}
		if err := service.store.DeleteLivingState(ctx, gitPreviewKind, state.Scope); err != nil {
			return err
		}
	}
	return nil
}

func gitPreviewScope(actor, project, preview uuid.UUID) string {
	return actor.String() + ":" + project.String() + ":" + preview.String()
}

func runGitBounded(ctx context.Context, root string, arguments ...string) ([]byte, bool, error) {
	return runGitCommand(ctx, root, true, arguments...)
}

func runGitMutableBounded(ctx context.Context, root string, arguments ...string) ([]byte, bool, error) {
	return runGitCommand(ctx, root, false, arguments...)
}

func runGitMutableInputBounded(ctx context.Context, root string, input []byte, arguments ...string) ([]byte, bool, error) {
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	argv := append([]string{"-c", "safe.directory=" + root, "-C", root}, arguments...)
	command := exec.CommandContext(runCtx, "git", argv...)
	command.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/nonexistent", "LANG=C.UTF-8",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0"}
	command.Stdin = bytes.NewReader(input)
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

func runGitCommand(ctx context.Context, root string, readOnly bool, arguments ...string) ([]byte, bool, error) {
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	argv := append([]string{"-c", "safe.directory=" + root, "-C", root}, arguments...)
	command := exec.CommandContext(runCtx, "git", argv...)
	optionalLocks := "1"
	if readOnly {
		optionalLocks = "0"
	}
	command.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/nonexistent", "LANG=C.UTF-8",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=" + optionalLocks, "GIT_TERMINAL_PROMPT=0"}
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

type boundedGitBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (buffer *boundedGitBuffer) Write(payload []byte) (int, error) {
	original := len(payload)
	remaining := maxGitOutput - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.truncated = true
		return original, nil
	}
	if len(payload) > remaining {
		payload, buffer.truncated = payload[:remaining], true
	}
	_, _ = buffer.buffer.Write(payload)
	return original, nil
}

func (buffer *boundedGitBuffer) Bytes() []byte { return buffer.buffer.Bytes() }

func parseGitStatus(raw []byte) []GitStatusEntry {
	fields := bytes.Split(raw, []byte{0})
	result := make([]GitStatusEntry, 0, len(fields))
	for index := 0; index < len(fields); index++ {
		field := string(fields[index])
		if len(field) < 3 {
			continue
		}
		entry := GitStatusEntry{IndexStatus: field[:1], WorkStatus: field[1:2], Path: field[3:]}
		entry.Untracked, entry.Ignored = field[:2] == "??", field[:2] == "!!"
		if (entry.IndexStatus == "R" || entry.IndexStatus == "C") && index+1 < len(fields) {
			entry.OriginalPath = string(fields[index+1])
			index++
		}
		result = append(result, entry)
	}
	return result
}

func gitBranches(ctx context.Context, root string) ([]GitBranch, error) {
	output, _, err := runGitBounded(ctx, root, "for-each-ref", "--format=%(refname:short)%00%(objectname)%00%(upstream:short)%00%(HEAD)",
		"refs/heads")
	if err != nil {
		return nil, err
	}
	result := []GitBranch{}
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		parts := bytes.Split(line, []byte{0})
		if len(parts) != 4 {
			continue
		}
		result = append(result, GitBranch{Name: string(parts[0]), Commit: string(parts[1]),
			Upstream: string(parts[2]), Current: strings.TrimSpace(string(parts[3])) == "*"})
	}
	return result, nil
}

func gitRemotes(ctx context.Context, root string) ([]GitRemote, error) {
	output, _, err := runGitBounded(ctx, root, "remote")
	if err != nil {
		return nil, err
	}
	result := []GitRemote{}
	for _, rawName := range strings.Fields(string(output)) {
		remote := GitRemote{Name: rawName}
		if value, _, getErr := runGitBounded(ctx, root, "remote", "get-url", rawName); getErr == nil {
			remote.FetchURL = strings.TrimSpace(string(value))
		}
		if value, _, getErr := runGitBounded(ctx, root, "remote", "get-url", "--push", rawName); getErr == nil {
			remote.PushURL = strings.TrimSpace(string(value))
		}
		result = append(result, remote)
	}
	return result, nil
}

func gitHistory(ctx context.Context, root string, limit int) ([]GitCommit, error) {
	output, _, err := runGitBounded(ctx, root, "log", "-n", strconv.Itoa(limit), "--format=%H%x00%P%x00%an%x00%aI%x00%s")
	if err != nil {
		return nil, err
	}
	result := []GitCommit{}
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		parts := bytes.SplitN(line, []byte{0}, 5)
		if len(parts) != 5 {
			continue
		}
		when, _ := time.Parse(time.RFC3339, string(parts[3]))
		result = append(result, GitCommit{Hash: string(parts[0]), Parents: strings.Fields(string(parts[1])),
			Author: string(parts[2]), AuthoredAt: when, Subject: string(parts[4])})
	}
	return result, nil
}

func parseGitBlame(raw []byte, start int) []GitBlameLine {
	lines := strings.Split(string(raw), "\n")
	result := []GitBlameLine{}
	current := GitBlameLine{Line: start}
	for _, line := range lines {
		if len(line) >= 40 && !strings.Contains(line[:40], " ") {
			current.Commit = line[:40]
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				current.Line, _ = strconv.Atoi(fields[2])
			}
		} else if strings.HasPrefix(line, "author ") {
			current.Author = strings.TrimPrefix(line, "author ")
		} else if strings.HasPrefix(line, "author-time ") {
			seconds, _ := strconv.ParseInt(strings.TrimPrefix(line, "author-time "), 10, 64)
			current.AuthoredAt = time.Unix(seconds, 0).UTC()
		} else if strings.HasPrefix(line, "\t") {
			current.Text = strings.TrimPrefix(line, "\t")
			result = append(result, current)
			current = GitBlameLine{}
		}
	}
	return result
}

func redactGitOutput(raw []byte) string {
	redacted, _ := redactSecrets("git-output", raw)
	return string(redacted)
}
