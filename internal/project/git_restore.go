package project

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

func (service *Service) PlanGitRestore(ctx context.Context, actor uuid.UUID,
	request GitRestorePlanRequest) (PatchSet, error) {
	project, root, err := service.gitProject(ctx, actor, request.ProjectID)
	if err != nil {
		return PatchSet{}, err
	}
	if project.WorkspaceRevision != request.WorkspaceRevision {
		return PatchSet{}, ErrStaleRevision
	}
	revision := strings.TrimSpace(request.Revision)
	if revision == "" || len(revision) > 200 || strings.ContainsAny(revision, "\x00\r\n") {
		return PatchSet{}, fmt.Errorf("project: bounded restore revision is required")
	}
	verified, _, err := runGitBounded(ctx, root, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return PatchSet{}, err
	}
	commit := strings.TrimSpace(string(verified))
	desiredPaths, err := gitTreePaths(ctx, root, commit)
	if err != nil {
		return PatchSet{}, err
	}
	currentHead, _, err := runGitBounded(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return PatchSet{}, err
	}
	currentPaths, err := gitTreePaths(ctx, root, strings.TrimSpace(string(currentHead)))
	if err != nil {
		return PatchSet{}, err
	}
	selected := map[string]struct{}{}
	if len(request.Paths) > 0 {
		if len(request.Paths) > maxPatchMembers {
			return PatchSet{}, fmt.Errorf("project: restore path limit exceeded")
		}
		for _, item := range request.Paths {
			item = cleanRelativePath(item)
			if item == "" {
				return PatchSet{}, ErrProtectedPath
			}
			selected[item] = struct{}{}
		}
	} else {
		for _, item := range desiredPaths {
			selected[item] = struct{}{}
		}
		for _, item := range currentPaths {
			selected[item] = struct{}{}
		}
	}
	desired := map[string]bool{}
	for _, item := range desiredPaths {
		desired[item] = true
	}
	currentTracked := map[string]bool{}
	for _, item := range currentPaths {
		currentTracked[item] = true
	}
	paths := make([]string, 0, len(selected))
	for item := range selected {
		paths = append(paths, item)
	}
	sort.Strings(paths)
	patch := PatchSet{Version: PatchSetVersion, ID: uuid.New(), ProjectID: project.ID,
		BaselineRevision: project.WorkspaceRevision,
		Criteria:         []string{"restore reviewed code from Git commit " + commit},
		ValidationPlan:   []string{"compare restored file hashes to Git blobs", "preserve every unselected and untracked path"},
		Members:          []PatchMember{}}
	total := 0
	for _, relative := range paths {
		if protectedPathReason(relative, nil) != "" {
			return PatchSet{}, fmt.Errorf("%w: protected restore path %s", ErrProtectedPath, relative)
		}
		absolute, err := securePatchPath(root, relative, true)
		if err != nil {
			return PatchSet{}, err
		}
		currentDigest, currentContent, _, currentErr := snapshotPath(absolute)
		if errors.Is(currentErr, os.ErrNotExist) {
			currentDigest, currentContent, currentErr = absentHash, nil, nil
		}
		if currentErr != nil {
			return PatchSet{}, currentErr
		}
		if !desired[relative] {
			if currentTracked[relative] && currentDigest != absentHash {
				patch.Members = append(patch.Members, PatchMember{Operation: PatchDelete, Path: relative,
					ExpectedSHA256: currentDigest})
			}
			continue
		}
		content, truncated, err := runGitBounded(ctx, root, "show", commit+":"+relative)
		if err != nil || truncated {
			return PatchSet{}, errors.Join(fmt.Errorf("project: Git restore blob is unavailable or oversized"), err)
		}
		total += len(content)
		if total > maxPatchBytes {
			return PatchSet{}, fmt.Errorf("project: restore exceeds bounded patch size")
		}
		if bytes.Equal(content, currentContent) {
			continue
		}
		member := PatchMember{Operation: PatchWrite, Path: relative, ExpectedSHA256: currentDigest}
		if utf8.Valid(content) && bytes.IndexByte(content, 0) < 0 {
			member.Content = string(content)
		} else {
			member.ContentBase64 = base64.StdEncoding.EncodeToString(content)
			member.MediaType = "application/octet-stream"
		}
		patch.Members = append(patch.Members, member)
	}
	if len(patch.Members) == 0 {
		return PatchSet{}, ErrConflict
	}
	if len(patch.Members) > maxPatchMembers {
		return PatchSet{}, fmt.Errorf("project: restore exceeds bounded patch members")
	}
	return patch, nil
}

func gitTreePaths(ctx context.Context, root, commit string) ([]string, error) {
	output, truncated, err := runGitBounded(ctx, root, "ls-tree", "-r", "-z", "--name-only", commit)
	if err != nil || truncated {
		return nil, errors.Join(fmt.Errorf("project: Git tree inventory is unavailable or oversized"), err)
	}
	result := []string{}
	for _, item := range bytes.Split(output, []byte{0}) {
		if len(item) == 0 {
			continue
		}
		result = append(result, string(item))
	}
	return result, nil
}
