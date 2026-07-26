package project

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const gitReviewCommentKind = "project_git_review_comments_v1"

type GitReviewRequest struct {
	ProjectID      uuid.UUID         `json:"project_id"`
	CriteriaByPath map[string]string `json:"criteria_by_path,omitempty"`
}

type GitReviewFile struct {
	Path          string   `json:"path"`
	OriginalPath  string   `json:"original_path,omitempty"`
	Criterion     string   `json:"criterion"`
	Subsystem     string   `json:"subsystem"`
	Kinds         []string `json:"kinds"`
	IndexStatus   string   `json:"index_status"`
	WorkStatus    string   `json:"work_status"`
	CurrentSHA256 string   `json:"current_sha256"`
	Diff          string   `json:"diff,omitempty"`
	DiffTruncated bool     `json:"diff_truncated"`
}

type GitReviewGroup struct {
	Criterion string          `json:"criterion"`
	Files     []GitReviewFile `json:"files"`
}

type GitChangeReview struct {
	Version           string           `json:"version"`
	ProjectID         uuid.UUID        `json:"project_id"`
	WorkspaceRevision uint64           `json:"workspace_revision"`
	Head              string           `json:"head"`
	Groups            []GitReviewGroup `json:"groups"`
	CreatedAt         time.Time        `json:"created_at"`
}

type GitReviewComment struct {
	ID         uuid.UUID  `json:"id"`
	ActorID    uuid.UUID  `json:"actor_id"`
	ProjectID  uuid.UUID  `json:"project_id"`
	Path       string     `json:"path"`
	Line       int        `json:"line,omitempty"`
	Criterion  string     `json:"criterion,omitempty"`
	Body       string     `json:"body"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

type GitReviewCommentInput struct {
	ProjectID uuid.UUID `json:"project_id"`
	Path      string    `json:"path"`
	Line      int       `json:"line,omitempty"`
	Criterion string    `json:"criterion,omitempty"`
	Body      string    `json:"body"`
}

type GitReviewResolveInput struct {
	ProjectID uuid.UUID `json:"project_id"`
	CommentID uuid.UUID `json:"comment_id"`
}

type gitReviewComments struct {
	Comments []GitReviewComment `json:"comments"`
}

func (service *Service) BuildGitReview(ctx context.Context, actor uuid.UUID,
	request GitReviewRequest) (GitChangeReview, error) {
	projection, err := service.GitProjection(ctx, actor, request.ProjectID)
	if err != nil {
		return GitChangeReview{}, err
	}
	groups := map[string][]GitReviewFile{}
	for _, status := range projection.Status {
		criterion := strings.TrimSpace(request.CriteriaByPath[status.Path])
		if criterion == "" {
			criterion = "unmapped"
		}
		file := GitReviewFile{Path: status.Path, OriginalPath: status.OriginalPath,
			Criterion: criterion, Subsystem: reviewSubsystem(status.Path),
			Kinds: reviewKinds(projection.RepositoryRoot, status), IndexStatus: status.IndexStatus,
			WorkStatus: status.WorkStatus, CurrentSHA256: absentHash}
		absolute, pathErr := securePatchPath(projection.RepositoryRoot, status.Path, true)
		if pathErr == nil {
			if digest, _, _, digestErr := snapshotPath(absolute); digestErr == nil {
				file.CurrentSHA256 = digest
			}
		}
		diff, truncated, diffErr := runGitBounded(ctx, projection.RepositoryRoot, "diff", "HEAD",
			"--find-renames", "--binary", "--no-ext-diff", "--", status.Path)
		if diffErr == nil && (len(diff) > 0 || !status.Untracked) {
			file.Diff, file.DiffTruncated = redactGitOutput(diff), truncated
		} else if status.Untracked && pathErr == nil {
			content, readErr := os.ReadFile(absolute)
			if readErr == nil && len(content) <= maxPatchBytes {
				redacted, _ := redactSecrets("untracked-review", content)
				file.Diff = "--- /dev/null\n+++ b/" + status.Path + "\n" + string(redacted)
			}
		}
		groups[criterion] = append(groups[criterion], file)
	}
	criteria := make([]string, 0, len(groups))
	for criterion := range groups {
		criteria = append(criteria, criterion)
	}
	sort.Strings(criteria)
	result := GitChangeReview{Version: GitContractVersion, ProjectID: projection.ProjectID,
		WorkspaceRevision: projection.WorkspaceRevision, Head: projection.Head,
		Groups: []GitReviewGroup{}, CreatedAt: service.clock.Now().UTC()}
	for _, criterion := range criteria {
		files := groups[criterion]
		sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
		result.Groups = append(result.Groups, GitReviewGroup{Criterion: criterion, Files: files})
	}
	return result, nil
}

func reviewSubsystem(relative string) string {
	clean := filepath.ToSlash(filepath.Clean(relative))
	if index := strings.IndexByte(clean, '/'); index >= 0 {
		return clean[:index]
	}
	return "root"
}

func reviewKinds(root string, status GitStatusEntry) []string {
	kinds := []string{}
	lower := strings.ToLower(filepath.ToSlash(status.Path))
	if status.OriginalPath != "" {
		kinds = append(kinds, "rename")
	}
	if strings.Contains(lower, "generated") || strings.HasSuffix(lower, ".gen.go") ||
		strings.Contains(lower, "/dist/") {
		kinds = append(kinds, "generated")
	}
	if strings.Contains(lower, "lock") || strings.HasSuffix(lower, "go.sum") || strings.HasSuffix(lower, "package.json") {
		kinds = append(kinds, "dependency")
	}
	if strings.Contains(lower, "migrat") || strings.Contains(lower, "schema") {
		kinds = append(kinds, "migration")
	}
	if strings.Contains(lower, "test") || strings.Contains(lower, "spec") {
		kinds = append(kinds, "test")
	}
	if protectedPathReason(status.Path, nil) != "" {
		kinds = append(kinds, "secret-risk")
	}
	if absolute, err := securePatchPath(root, status.Path, false); err == nil {
		if content, readErr := os.ReadFile(absolute); readErr == nil && bytes.IndexByte(content, 0) >= 0 {
			kinds = append(kinds, "binary")
		}
	}
	if len(kinds) == 0 {
		kinds = append(kinds, "source")
	}
	return kinds
}

func (service *Service) AddGitReviewComment(ctx context.Context, actor uuid.UUID,
	input GitReviewCommentInput) (GitReviewComment, error) {
	if _, _, err := service.gitProject(ctx, actor, input.ProjectID); err != nil {
		return GitReviewComment{}, err
	}
	input.Path = cleanRelativePath(input.Path)
	input.Body = strings.TrimSpace(input.Body)
	if input.Path == "" || input.Body == "" || len(input.Body) > 16<<10 || input.Line < 0 {
		return GitReviewComment{}, fmt.Errorf("project: bounded review comment is required")
	}
	comments, err := service.loadGitReviewComments(ctx, actor, input.ProjectID)
	if err != nil {
		return GitReviewComment{}, err
	}
	if len(comments.Comments) >= 1024 {
		return GitReviewComment{}, fmt.Errorf("project: review comment limit reached")
	}
	comment := GitReviewComment{ID: uuid.New(), ActorID: actor, ProjectID: input.ProjectID,
		Path: input.Path, Line: input.Line, Criterion: strings.TrimSpace(input.Criterion),
		Body: input.Body, CreatedAt: service.clock.Now().UTC()}
	comments.Comments = append(comments.Comments, comment)
	return comment, service.saveGitReviewComments(ctx, actor, input.ProjectID, comments)
}

func (service *Service) ListGitReviewComments(ctx context.Context, actor, projectID uuid.UUID) ([]GitReviewComment, error) {
	if _, _, err := service.gitProject(ctx, actor, projectID); err != nil {
		return nil, err
	}
	comments, err := service.loadGitReviewComments(ctx, actor, projectID)
	return comments.Comments, err
}

func (service *Service) ResolveGitReviewComment(ctx context.Context, actor uuid.UUID,
	input GitReviewResolveInput) (GitReviewComment, error) {
	comments, err := service.loadGitReviewComments(ctx, actor, input.ProjectID)
	if err != nil {
		return GitReviewComment{}, err
	}
	for index := range comments.Comments {
		if comments.Comments[index].ID != input.CommentID || comments.Comments[index].ActorID != actor {
			continue
		}
		if comments.Comments[index].ResolvedAt == nil {
			now := service.clock.Now().UTC()
			comments.Comments[index].ResolvedAt = &now
		}
		return comments.Comments[index], service.saveGitReviewComments(ctx, actor, input.ProjectID, comments)
	}
	return GitReviewComment{}, ErrNotFound
}

func (service *Service) loadGitReviewComments(ctx context.Context, actor, projectID uuid.UUID) (gitReviewComments, error) {
	result := gitReviewComments{Comments: []GitReviewComment{}}
	raw, err := service.store.LoadLivingState(ctx, gitReviewCommentKind, patchScope(actor, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if json.Unmarshal(raw, &result) != nil {
		return gitReviewComments{}, fmt.Errorf("project: invalid encrypted Git review comments")
	}
	return result, nil
}

func (service *Service) saveGitReviewComments(ctx context.Context, actor, projectID uuid.UUID,
	comments gitReviewComments) error {
	raw, err := json.Marshal(comments)
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(ctx, gitReviewCommentKind, patchScope(actor, projectID), raw)
}
