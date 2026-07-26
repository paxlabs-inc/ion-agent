package project

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const gitMutationJournalKind = "project_git_mutation_journal_v1"

type gitMutationJournal struct {
	Version           string             `json:"version"`
	ActorID           uuid.UUID          `json:"actor_id"`
	ProjectID         uuid.UUID          `json:"project_id"`
	WorkspaceRevision uint64             `json:"workspace_revision"`
	Operation         string             `json:"operation"`
	BeforeHead        string             `json:"before_head"`
	BeforeStatus      string             `json:"before_status"`
	IntendedRef       string             `json:"intended_ref,omitempty"`
	IntendedTarget    string             `json:"intended_target,omitempty"`
	State             string             `json:"state"`
	Receipt           GitMutationReceipt `json:"receipt"`
}

type gitRefIntent struct {
	Ref    string
	Target string
}

func (service *Service) CreateGitBranch(ctx context.Context, actor uuid.UUID,
	request GitBranchCreateRequest) (GitMutationReceipt, error) {
	name := strings.TrimSpace(request.Name)
	if !validGitRefInput(name) {
		return GitMutationReceipt{}, fmt.Errorf("project: valid branch name is required")
	}
	return service.applyGitMutation(ctx, actor, request.ProjectID, request.WorkspaceRevision,
		request.ExpectedHead, "branch.create", nil, PolicyYellow,
		gitRefIntent{Ref: "refs/heads/" + name, Target: request.ExpectedHead}, func(root string) error {
			if _, _, err := runGitBounded(ctx, root, "check-ref-format", "--branch", name); err != nil {
				return err
			}
			_, _, err := runGitMutableBounded(ctx, root, "branch", name, request.ExpectedHead)
			return err
		})
}

func (service *Service) StageGitPaths(ctx context.Context, actor uuid.UUID,
	request GitStageRequest) (GitMutationReceipt, error) {
	return service.applyGitMutation(ctx, actor, request.ProjectID, request.WorkspaceRevision,
		request.ExpectedHead, "stage", request.Paths, PolicyYellow, gitRefIntent{}, func(root string) error {
			argv := []string{"add", "-A", "--"}
			for _, item := range request.Paths {
				argv = append(argv, item.Path)
			}
			_, _, err := runGitMutableBounded(ctx, root, argv...)
			return err
		})
}

func (service *Service) StageGitHunks(ctx context.Context, actor uuid.UUID,
	request GitStageHunksRequest) (GitMutationReceipt, error) {
	if len(request.Patch) == 0 || len(request.Patch) > maxPatchBytes || len(request.Paths) == 0 ||
		len(request.DiffSHA256) != sha256.Size*2 {
		return GitMutationReceipt{}, fmt.Errorf("project: bounded selected Git patch and diff hash are required")
	}
	return service.applyGitMutation(ctx, actor, request.ProjectID, request.WorkspaceRevision,
		request.ExpectedHead, "stage.hunks", request.Paths, PolicyYellow, gitRefIntent{}, func(root string) error {
			pathNames := make([]string, 0, len(request.Paths))
			for _, item := range request.Paths {
				pathNames = append(pathNames, item.Path)
			}
			argv := append([]string{"diff", "--binary", "--no-ext-diff", "--"}, pathNames...)
			current, truncated, err := runGitBounded(ctx, root, argv...)
			if err != nil || truncated || byteDigest(current) != strings.ToLower(request.DiffSHA256) {
				return errors.Join(ErrConflict, err)
			}
			if err := validateSelectedGitPatch(root, []byte(request.Patch), pathNames); err != nil {
				return err
			}
			if _, _, err := runGitMutableInputBounded(ctx, root, []byte(request.Patch),
				"apply", "--cached", "--check", "--recount", "--unidiff-zero", "-"); err != nil {
				return err
			}
			_, _, err = runGitMutableInputBounded(ctx, root, []byte(request.Patch),
				"apply", "--cached", "--recount", "--unidiff-zero", "-")
			return err
		})
}

func (service *Service) CommitGitPaths(ctx context.Context, actor uuid.UUID,
	request GitCommitRequest, authorized bool) (GitMutationReceipt, error) {
	message := strings.TrimSpace(request.Message)
	if !authorized || message == "" || len(message) > 16<<10 || !validGitIdentity(request.AuthorName, request.AuthorEmail) {
		return GitMutationReceipt{}, fmt.Errorf("project: exact commit requires explicit approval and a bounded message")
	}
	return service.applyGitMutation(ctx, actor, request.ProjectID, request.WorkspaceRevision,
		request.ExpectedHead, "commit", request.Paths, PolicyRed, gitRefIntent{}, func(root string) error {
			argv := []string{"-c", "commit.gpgSign=false", "-c", "user.name=" + request.AuthorName,
				"-c", "user.email=" + request.AuthorEmail, "commit", "--only", "--no-verify", "-m", message, "--"}
			for _, item := range request.Paths {
				argv = append(argv, item.Path)
			}
			_, _, err := runGitMutableBounded(ctx, root, argv...)
			return err
		})
}

func (service *Service) CreateGitCheckpoint(ctx context.Context, actor uuid.UUID,
	request GitCheckpointRequest, authorized bool) (GitMutationReceipt, error) {
	message := strings.TrimSpace(request.Message)
	if !authorized || message == "" || len(message) > 16<<10 || !validGitIdentity(request.AuthorName, request.AuthorEmail) {
		return GitMutationReceipt{}, fmt.Errorf("project: checkpoint requires explicit approval and a bounded message")
	}
	return service.applyGitMutation(ctx, actor, request.ProjectID, request.WorkspaceRevision,
		request.ExpectedHead, "checkpoint", request.Paths, PolicyRed, gitRefIntent{}, func(root string) error {
			argv := []string{"-c", "commit.gpgSign=false", "-c", "user.name=" + request.AuthorName,
				"-c", "user.email=" + request.AuthorEmail, "commit", "--only", "--no-verify", "-m", message, "--"}
			for _, item := range request.Paths {
				argv = append(argv, item.Path)
			}
			_, _, err := runGitMutableBounded(ctx, root, argv...)
			return err
		})
}

func (service *Service) CreateGitTag(ctx context.Context, actor uuid.UUID,
	request GitTagRequest, authorized bool) (GitMutationReceipt, error) {
	name, message := strings.TrimSpace(request.Name), strings.TrimSpace(request.Message)
	if !authorized || !validGitRefInput(name) || message == "" || len(message) > 16<<10 ||
		!validGitIdentity(request.AuthorName, request.AuthorEmail) {
		return GitMutationReceipt{}, fmt.Errorf("project: annotated tag requires explicit approval")
	}
	return service.applyGitMutation(ctx, actor, request.ProjectID, request.WorkspaceRevision,
		request.ExpectedHead, "tag.create", nil, PolicyRed,
		gitRefIntent{Ref: "refs/tags/" + name, Target: request.ExpectedHead}, func(root string) error {
			if _, _, err := runGitBounded(ctx, root, "check-ref-format", "refs/tags/"+name); err != nil {
				return err
			}
			_, _, err := runGitMutableBounded(ctx, root, "-c", "tag.gpgSign=false", "-c", "user.name="+request.AuthorName,
				"-c", "user.email="+request.AuthorEmail, "tag", "-a", name,
				request.ExpectedHead, "-m", message)
			return err
		})
}

func (service *Service) applyGitMutation(ctx context.Context, actor, projectID uuid.UUID,
	workspaceRevision uint64, expectedHead, operation string, paths []GitPathExpectation,
	classification PolicyClassification, refIntent gitRefIntent, apply func(string) error) (GitMutationReceipt, error) {
	service.gitMu.Lock()
	defer service.gitMu.Unlock()
	project, root, err := service.gitProject(ctx, actor, projectID)
	if err != nil {
		return GitMutationReceipt{}, err
	}
	if project.WorkspaceRevision != workspaceRevision {
		return GitMutationReceipt{}, ErrStaleRevision
	}
	head, _, err := runGitBounded(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != strings.TrimSpace(expectedHead) {
		return GitMutationReceipt{}, errors.Join(ErrConflict, err)
	}
	if err := verifyGitPaths(root, paths); err != nil {
		return GitMutationReceipt{}, err
	}
	if _, err := service.store.LoadLivingState(ctx, gitMutationJournalKind, patchScope(actor, projectID)); err == nil {
		return GitMutationReceipt{}, ErrPatchPending
	} else if !errors.Is(err, sql.ErrNoRows) {
		return GitMutationReceipt{}, err
	}
	status, _, err := runGitBounded(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return GitMutationReceipt{}, err
	}
	journal := gitMutationJournal{Version: GitContractVersion, ActorID: actor, ProjectID: projectID,
		WorkspaceRevision: workspaceRevision, Operation: operation, BeforeHead: strings.TrimSpace(string(head)),
		BeforeStatus: byteDigest(status), IntendedRef: refIntent.Ref, IntendedTarget: refIntent.Target, State: "prepared"}
	if err := service.saveGitMutation(ctx, journal); err != nil {
		return GitMutationReceipt{}, err
	}
	if err := apply(root); err != nil {
		_ = service.store.DeleteLivingState(context.Background(), gitMutationJournalKind, patchScope(actor, projectID))
		return GitMutationReceipt{}, err
	}
	afterHead, _, err := runGitBounded(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return GitMutationReceipt{}, err
	}
	receipt := GitMutationReceipt{Version: GitContractVersion, ProjectID: projectID, Operation: operation,
		BeforeHead: journal.BeforeHead, AfterHead: strings.TrimSpace(string(afterHead)), Paths: append([]GitPathExpectation(nil), paths...),
		Classification: classification, AppliedAt: service.clock.Now().UTC()}
	journal.State, journal.Receipt = "applied", receipt
	if err := service.saveGitMutation(ctx, journal); err != nil {
		return GitMutationReceipt{}, err
	}
	project, err = service.bumpWorkspaceRevision(ctx, actor, projectID, workspaceRevision)
	if err != nil {
		return GitMutationReceipt{}, err
	}
	receipt.WorkspaceRevision = project.WorkspaceRevision
	journal.State, journal.Receipt = "committed", receipt
	if err := service.saveGitMutation(ctx, journal); err != nil {
		return GitMutationReceipt{}, err
	}
	if err := service.store.DeleteLivingState(ctx, gitMutationJournalKind, patchScope(actor, projectID)); err != nil {
		return GitMutationReceipt{}, err
	}
	return receipt, nil
}

func verifyGitPaths(root string, paths []GitPathExpectation) error {
	if len(paths) == 0 {
		return nil
	}
	if len(paths) > maxPatchMembers {
		return fmt.Errorf("project: too many Git paths")
	}
	seen := map[string]struct{}{}
	for index := range paths {
		paths[index].Path = cleanRelativePath(paths[index].Path)
		if paths[index].Path == "" || paths[index].SHA256 == "" {
			return fmt.Errorf("project: exact Git path expectations are required")
		}
		if _, duplicate := seen[paths[index].Path]; duplicate {
			return fmt.Errorf("project: duplicate Git path")
		}
		seen[paths[index].Path] = struct{}{}
		path, err := securePatchPath(root, paths[index].Path, true)
		if err != nil {
			return err
		}
		digest, _, _, err := snapshotPath(path)
		if errors.Is(err, os.ErrNotExist) {
			digest, err = absentHash, nil
		}
		if err != nil || digest != paths[index].SHA256 {
			return PatchConflict{Path: paths[index].Path, Expected: paths[index].SHA256, Actual: digest}
		}
	}
	return nil
}

func (service *Service) saveGitMutation(ctx context.Context, journal gitMutationJournal) error {
	raw, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(ctx, gitMutationJournalKind, patchScope(journal.ActorID, journal.ProjectID), raw)
}

func (service *Service) reconcileGitMutations(ctx context.Context) error {
	states, err := service.store.ListLivingStates(ctx, gitMutationJournalKind)
	if err != nil {
		return err
	}
	for _, state := range states {
		var journal gitMutationJournal
		if json.Unmarshal(state.State, &journal) != nil || journal.Version != GitContractVersion {
			return fmt.Errorf("project: invalid encrypted Git mutation journal")
		}
		project, root, err := service.gitProject(ctx, journal.ActorID, journal.ProjectID)
		if err != nil {
			return err
		}
		switch journal.State {
		case "prepared":
			head, _, headErr := runGitBounded(ctx, root, "rev-parse", "--verify", "HEAD")
			status, _, statusErr := runGitBounded(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
			if journal.IntendedRef != "" {
				resolved, _, refErr := runGitBounded(ctx, root, "rev-parse", "--verify", journal.IntendedRef+"^{}")
				if refErr == nil {
					if strings.TrimSpace(string(resolved)) != journal.IntendedTarget {
						return fmt.Errorf("%w: interrupted Git ref points to an unexpected target", ErrConflict)
					}
					journal.Receipt = GitMutationReceipt{Version: GitContractVersion, ProjectID: journal.ProjectID,
						Operation: journal.Operation, BeforeHead: journal.BeforeHead, AfterHead: journal.BeforeHead,
						Classification: classificationForGitOperation(journal.Operation), AppliedAt: service.clock.Now().UTC()}
					journal.State = "applied"
					if err := service.saveGitMutation(ctx, journal); err != nil {
						return err
					}
					if project.WorkspaceRevision == journal.WorkspaceRevision {
						if _, err := service.bumpWorkspaceRevision(ctx, journal.ActorID, journal.ProjectID, journal.WorkspaceRevision); err != nil {
							return err
						}
					}
					break
				}
			}
			if headErr != nil || statusErr != nil || strings.TrimSpace(string(head)) != journal.BeforeHead || byteDigest(status) != journal.BeforeStatus {
				return fmt.Errorf("%w: interrupted Git mutation requires review", ErrConflict)
			}
		case "applied", "committed":
			if project.WorkspaceRevision == journal.WorkspaceRevision {
				if _, err := service.bumpWorkspaceRevision(ctx, journal.ActorID, journal.ProjectID, journal.WorkspaceRevision); err != nil {
					return err
				}
			} else if project.WorkspaceRevision != journal.WorkspaceRevision+1 {
				return ErrConflict
			}
		default:
			return fmt.Errorf("project: invalid Git mutation state")
		}
		if err := service.store.DeleteLivingState(ctx, gitMutationJournalKind, state.Scope); err != nil {
			return err
		}
	}
	return nil
}

func classificationForGitOperation(operation string) PolicyClassification {
	if operation == "branch.create" || operation == "stage" || operation == "stage.hunks" {
		return PolicyYellow
	}
	return PolicyRed
}

func validateSelectedGitPatch(root string, patch []byte, allowed []string) error {
	allowedPaths := map[string]struct{}{}
	for _, path := range allowed {
		allowedPaths[filepath.ToSlash(cleanRelativePath(path))] = struct{}{}
	}
	seen := map[string]struct{}{}
	for _, line := range strings.Split(string(patch), "\n") {
		if !strings.HasPrefix(line, "--- ") && !strings.HasPrefix(line, "+++ ") {
			continue
		}
		value := strings.TrimSpace(line[4:])
		if value == "/dev/null" {
			continue
		}
		if strings.ContainsAny(value, "\t\r\n\"") || (!strings.HasPrefix(value, "a/") && !strings.HasPrefix(value, "b/")) {
			return fmt.Errorf("project: selected Git patch contains an unsupported path")
		}
		relative := cleanRelativePath(value[2:])
		if _, ok := allowedPaths[relative]; !ok {
			return fmt.Errorf("project: selected Git patch escapes reviewed paths")
		}
		if _, err := securePatchPath(root, relative, true); err != nil {
			return err
		}
		seen[relative] = struct{}{}
	}
	if len(seen) == 0 {
		return fmt.Errorf("project: selected Git patch has no reviewed path headers")
	}
	return nil
}

func byteDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validGitRefInput(value string) bool {
	return value != "" && len(value) <= 200 && !strings.HasPrefix(value, "-") && !strings.ContainsAny(value, "\x00\r\n")
}

func validGitIdentity(name, email string) bool {
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	return name != "" && email != "" && len(name) <= 200 && len(email) <= 320 && strings.Contains(email, "@") &&
		!strings.ContainsAny(name+email, "\x00\r\n")
}
