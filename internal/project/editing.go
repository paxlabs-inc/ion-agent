package project

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	patchJournalKind = "project_patch_journal_v1"
	patchHistoryKind = "project_patch_history_v1"
	maxPatchMembers  = 128
	maxPatchBytes    = 512 << 10
	absentHash       = "absent"
	directoryHash    = "directory"
)

type patchSnapshot struct {
	Path    string      `json:"path"`
	Exists  bool        `json:"exists"`
	IsDir   bool        `json:"is_dir,omitempty"`
	Mode    os.FileMode `json:"mode"`
	Content []byte      `json:"content,omitempty"`
}

type patchJournal struct {
	Version string            `json:"version"`
	ActorID uuid.UUID         `json:"actor_id"`
	Patch   PatchSet          `json:"patch"`
	State   string            `json:"state"`
	Before  []patchSnapshot   `json:"before"`
	After   map[string]string `json:"after"`
	Receipt PatchReceipt      `json:"receipt"`
}

type patchRecord struct {
	Receipt PatchReceipt    `json:"receipt"`
	Before  []patchSnapshot `json:"before,omitempty"`
}

type patchHistory struct {
	Records []patchRecord `json:"records"`
}

type EditingService struct {
	mu          sync.Mutex
	store       *session.Store
	clock       types.Clock
	projects    *Service
	beforeApply func()
}

func newEditingService(store *session.Store, clock types.Clock, projects *Service) *EditingService {
	return &EditingService{store: store, clock: clock, projects: projects}
}

func (service *EditingService) Apply(ctx context.Context, actor uuid.UUID, patch PatchSet) (PatchReceipt, error) {
	return service.apply(ctx, actor, patch, false)
}

func (service *EditingService) ApplyApproved(ctx context.Context, actor uuid.UUID, patch PatchSet) (PatchReceipt, error) {
	return service.apply(ctx, actor, patch, true)
}

func (service *EditingService) apply(ctx context.Context, actor uuid.UUID, patch PatchSet, approved bool) (PatchReceipt, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := validatePatchSet(actor, patch, approved); err != nil {
		return PatchReceipt{}, err
	}
	project, err := service.projects.Get(ctx, actor, patch.ProjectID)
	if err != nil {
		return PatchReceipt{}, err
	}
	if project.WorkspaceRevision != patch.BaselineRevision {
		return PatchReceipt{}, ErrStaleRevision
	}
	if existing, err := service.load(ctx, actor, patch.ProjectID); err == nil {
		if existing.State != "committed" {
			return PatchReceipt{}, ErrPatchPending
		}
		if err := service.store.DeleteLivingState(ctx, patchJournalKind, patchScope(actor, patch.ProjectID)); err != nil {
			return PatchReceipt{}, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return PatchReceipt{}, err
	}
	before, err := preflightPatch(project.Root, &patch)
	if err != nil {
		return PatchReceipt{}, err
	}
	after, err := predictPatchPostimages(patch, before)
	if err != nil {
		return PatchReceipt{}, err
	}
	journal := patchJournal{Version: PatchSetVersion, ActorID: actor, Patch: patch,
		State: "prepared", Before: before, After: after}
	if err := service.save(ctx, actor, patch.ProjectID, journal); err != nil {
		return PatchReceipt{}, err
	}
	if service.beforeApply != nil {
		service.beforeApply()
	}
	if err := applyPatchMembers(ctx, project.Root, patch.Members); err != nil {
		rollbackErr := restoreSnapshotsGuarded(project.Root, before, after)
		if rollbackErr == nil {
			_ = service.store.DeleteLivingState(context.Background(), patchJournalKind, patchScope(actor, patch.ProjectID))
		}
		return PatchReceipt{}, errors.Join(err, rollbackErr)
	}
	receipt, err := buildPatchReceipt(project.Root, patch, service.clock.Now().UTC())
	if err != nil {
		return PatchReceipt{}, errors.Join(err, restoreSnapshotsGuarded(project.Root, before, after))
	}
	journal.State, journal.Receipt = "applied", receipt
	if err := service.save(ctx, actor, patch.ProjectID, journal); err != nil {
		return PatchReceipt{}, err
	}
	project, err = service.projects.bumpWorkspaceRevision(ctx, actor, patch.ProjectID, patch.BaselineRevision)
	if err != nil {
		return PatchReceipt{}, err
	}
	receipt.WorkspaceRevision, receipt.Status = project.WorkspaceRevision, "committed"
	journal.State, journal.Receipt = "committed", receipt
	if err := service.save(ctx, actor, patch.ProjectID, journal); err != nil {
		return PatchReceipt{}, err
	}
	if err := service.record(ctx, actor, patch.ProjectID, receipt, before); err != nil {
		return PatchReceipt{}, err
	}
	if err := service.store.DeleteLivingState(ctx, patchJournalKind, patchScope(actor, patch.ProjectID)); err != nil {
		return PatchReceipt{}, err
	}
	return receipt, nil
}

func (service *EditingService) ReconcileAll(ctx context.Context) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	states, err := service.store.ListLivingStates(ctx, patchJournalKind)
	if err != nil {
		return err
	}
	for _, state := range states {
		var journal patchJournal
		if json.Unmarshal(state.State, &journal) != nil || journal.Version != PatchSetVersion {
			return fmt.Errorf("project: invalid encrypted patch journal")
		}
		project, getErr := service.projects.Get(ctx, journal.ActorID, journal.Patch.ProjectID)
		if getErr != nil {
			return getErr
		}
		switch journal.State {
		case "prepared":
			if err := restoreSnapshotsGuarded(project.Root, journal.Before, journal.After); err != nil {
				return err
			}
			if err := service.store.DeleteLivingState(ctx, patchJournalKind, state.Scope); err != nil {
				return err
			}
		case "applied":
			if project.WorkspaceRevision == journal.Patch.BaselineRevision {
				if _, err := service.projects.bumpWorkspaceRevision(ctx, journal.ActorID,
					journal.Patch.ProjectID, journal.Patch.BaselineRevision); err != nil {
					return err
				}
			} else if project.WorkspaceRevision != journal.Patch.BaselineRevision+1 {
				return ErrConflict
			}
			journal.State, journal.Receipt.Status = "committed", "committed"
			journal.Receipt.WorkspaceRevision = journal.Patch.BaselineRevision + 1
			if err := service.save(ctx, journal.ActorID, journal.Patch.ProjectID, journal); err != nil {
				return err
			}
			if err := service.record(ctx, journal.ActorID, journal.Patch.ProjectID, journal.Receipt, journal.Before); err != nil {
				return err
			}
			if err := service.store.DeleteLivingState(ctx, patchJournalKind, state.Scope); err != nil {
				return err
			}
		case "committed":
			if err := service.record(ctx, journal.ActorID, journal.Patch.ProjectID, journal.Receipt, journal.Before); err != nil {
				return err
			}
			if err := service.store.DeleteLivingState(ctx, patchJournalKind, state.Scope); err != nil {
				return err
			}
		default:
			return fmt.Errorf("project: invalid patch journal state")
		}
	}
	return nil
}

func validatePatchSet(actor uuid.UUID, patch PatchSet, approved bool) error {
	if actor == uuid.Nil || patch.Version != PatchSetVersion || patch.ID == uuid.Nil ||
		patch.ProjectID == uuid.Nil || patch.BaselineRevision == 0 || len(patch.Members) == 0 ||
		len(patch.Members) > maxPatchMembers || len(patch.Criteria) == 0 || len(patch.ValidationPlan) == 0 {
		return fmt.Errorf("project: complete bounded patch set is required")
	}
	total := 0
	affected := map[string]struct{}{}
	for _, member := range patch.Members {
		total += len(member.Content) + len(member.ContentBase64) + len(member.OldText) + len(member.NewText) + len(member.JSONValue)
		if total > maxPatchBytes || strings.TrimSpace(member.Path) == "" || strings.TrimSpace(member.ExpectedSHA256) == "" {
			return fmt.Errorf("project: patch set exceeds bounded mutation limits")
		}
		switch member.Operation {
		case PatchWrite, PatchExact, PatchDelete, PatchRename, PatchCopy, PatchMkdir, PatchArchive, PatchJSONSet:
		default:
			return fmt.Errorf("project: unsupported patch operation %q", member.Operation)
		}
		if member.Content != "" && member.ContentBase64 != "" {
			return fmt.Errorf("project: patch member cannot contain text and binary content")
		}
		if member.ContentBase64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(member.ContentBase64)
			if err != nil || len(decoded) > maxPatchBytes || !approved {
				return fmt.Errorf("project: binary or media writes require bounded decoded content and explicit approval")
			}
		}
		if member.Operation == PatchArchive {
			if len(member.ArchivePaths) == 0 || len(member.ArchivePaths) > maxPatchMembers || member.ExpectedSHA256 != absentHash {
				return fmt.Errorf("project: archive creation requires bounded sources and an absent output preimage")
			}
			for _, source := range member.ArchivePaths {
				if cleanRelativePath(source) == "" {
					return ErrProtectedPath
				}
			}
		}
		if member.Operation == PatchJSONSet {
			var value any
			if member.JSONPointer == "" || len(member.JSONValue) == 0 || json.Unmarshal(member.JSONValue, &value) != nil {
				return fmt.Errorf("project: structured JSON patches require a valid pointer and value")
			}
		}
		for _, candidate := range []string{member.Path, member.Destination} {
			if candidate == "" {
				continue
			}
			candidate = cleanRelativePath(candidate)
			if candidate == "" {
				return ErrProtectedPath
			}
			if _, duplicate := affected[candidate]; duplicate {
				return fmt.Errorf("project: patch paths must be unique: %s", candidate)
			}
			affected[candidate] = struct{}{}
		}
	}
	_, requiresApproval := classifyPatch(patch)
	if requiresApproval && !approved {
		return fmt.Errorf("project: RED patch operations require explicit approval")
	}
	return nil
}

func classifyPatch(patch PatchSet) (PolicyClassification, bool) {
	classification := PolicyYellow
	for _, member := range patch.Members {
		if member.Operation == PatchDelete || member.Operation == PatchRename || member.Operation == PatchArchive ||
			member.ContentBase64 != "" || protectedPathReason(cleanRelativePath(member.Path), nil) != "" {
			return PolicyRed, true
		}
	}
	return classification, false
}

func preflightPatch(root string, patch *PatchSet) ([]patchSnapshot, error) {
	paths := map[string]struct{}{}
	for index := range patch.Members {
		member := &patch.Members[index]
		paths[member.Path] = struct{}{}
		if member.Destination != "" {
			paths[member.Destination] = struct{}{}
		}
		path, err := securePatchPath(root, member.Path, member.Operation == PatchWrite || member.Operation == PatchMkdir || member.Operation == PatchArchive)
		if err != nil {
			return nil, err
		}
		digest, _, _, err := snapshotPath(path)
		if errors.Is(err, os.ErrNotExist) && digest == absentHash {
			err = nil
		}
		if err != nil {
			return nil, errors.Join(ErrStalePreimage, err)
		}
		if digest != member.ExpectedSHA256 {
			return nil, PatchConflict{Path: member.Path, Expected: member.ExpectedSHA256, Actual: digest}
		}
		if member.Destination != "" {
			destination, err := securePatchPath(root, member.Destination, true)
			if err != nil {
				return nil, err
			}
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				return nil, ErrConflict
			}
		}
		if member.Operation == PatchArchive {
			archive, err := buildArchive(root, member.ArchivePaths)
			if err != nil {
				return nil, err
			}
			digest := sha256.Sum256(archive)
			member.ArchiveBaselineSHA256 = hex.EncodeToString(digest[:])
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	result := make([]patchSnapshot, 0, len(ordered))
	for _, relative := range ordered {
		absolute, err := securePatchPath(root, relative, true)
		if err != nil {
			return nil, err
		}
		_, content, mode, snapErr := snapshotPath(absolute)
		if snapErr != nil && !errors.Is(snapErr, os.ErrNotExist) {
			return nil, snapErr
		}
		result = append(result, patchSnapshot{Path: cleanRelativePath(relative), Exists: snapErr == nil,
			IsDir: snapErr == nil && content == nil && mode&os.ModeDir != 0, Mode: mode, Content: content})
	}
	return result, nil
}

func snapshotPath(path string) (string, []byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return absentHash, nil, 0, err
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		if err == nil && info.IsDir() {
			return directoryHash, nil, os.ModeDir | info.Mode().Perm(), nil
		}
		return "", nil, 0, errors.Join(ErrProtectedPath, err)
	}
	if info.Size() > maxPatchBytes {
		return "", nil, 0, fmt.Errorf("project: patch target exceeds size limit")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, 0, err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), content, info.Mode().Perm(), nil
}

func applyPatchMembers(ctx context.Context, root string, members []PatchMember) error {
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := securePatchPath(root, member.Path, member.Operation == PatchWrite || member.Operation == PatchMkdir || member.Operation == PatchArchive)
		if err != nil {
			return err
		}
		actual, _, _, hashErr := snapshotPath(path)
		if errors.Is(hashErr, os.ErrNotExist) && actual == absentHash {
			hashErr = nil
		}
		if hashErr != nil {
			return hashErr
		}
		if actual != member.ExpectedSHA256 {
			return PatchConflict{Path: member.Path, Expected: member.ExpectedSHA256, Actual: actual}
		}
		switch member.Operation {
		case PatchWrite:
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			mode := os.FileMode(0o600)
			if info, statErr := os.Lstat(path); statErr == nil {
				if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					return ErrProtectedPath
				}
				mode = info.Mode().Perm()
			}
			content := []byte(member.Content)
			if member.ContentBase64 != "" {
				content, err = base64.StdEncoding.DecodeString(member.ContentBase64)
				if err != nil {
					return err
				}
			}
			if err := writeProjectAtomic(path, content, mode); err != nil {
				return err
			}
		case PatchExact:
			content, err := os.ReadFile(path)
			if err != nil || len(member.OldText) == 0 {
				return errors.Join(ErrStalePreimage, err)
			}
			count := strings.Count(string(content), member.OldText)
			if count == 0 || count > 1 && !member.ReplaceAll {
				return ErrStalePreimage
			}
			limit := 1
			if member.ReplaceAll {
				limit = -1
			}
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return errors.Join(ErrProtectedPath, err)
			}
			if err := writeProjectAtomic(path, []byte(strings.Replace(string(content), member.OldText, member.NewText, limit)), info.Mode().Perm()); err != nil {
				return err
			}
		case PatchJSONSet:
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			updated, err := applyJSONPointer(content, member.JSONPointer, member.JSONValue)
			if err != nil {
				return err
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return errors.Join(ErrProtectedPath, err)
			}
			if err := writeProjectAtomic(path, updated, info.Mode().Perm()); err != nil {
				return err
			}
		case PatchDelete:
			if err := os.Remove(path); err != nil {
				return err
			}
		case PatchRename, PatchCopy:
			destination, err := securePatchPath(root, member.Destination, true)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return err
			}
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				return ErrConflict
			}
			if member.Operation == PatchRename {
				if err := os.Rename(path, destination); err != nil {
					return err
				}
			} else {
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				info, err := os.Lstat(path)
				if err != nil || info.Mode()&os.ModeSymlink != 0 {
					return errors.Join(ErrProtectedPath, err)
				}
				if err := writeProjectAtomic(destination, content, info.Mode().Perm()); err != nil {
					return err
				}
			}
		case PatchMkdir:
			if err := os.Mkdir(path, 0o700); err != nil {
				return err
			}
		case PatchArchive:
			archive, err := buildArchive(root, member.ArchivePaths)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(archive)
			if hex.EncodeToString(digest[:]) != member.ArchiveBaselineSHA256 {
				return PatchConflict{Path: member.Path, Expected: member.ArchiveBaselineSHA256,
					Actual: hex.EncodeToString(digest[:])}
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := writeProjectAtomic(path, archive, 0o600); err != nil {
				return err
			}
		default:
			return fmt.Errorf("project: unsupported patch operation %q", member.Operation)
		}
	}
	return nil
}

func predictPatchPostimages(patch PatchSet, snapshots []patchSnapshot) (map[string]string, error) {
	contents := map[string]patchSnapshot{}
	after := map[string]string{}
	for _, snapshot := range snapshots {
		contents[snapshot.Path] = snapshot
		after[snapshot.Path] = snapshotDigest(snapshot)
	}
	for _, member := range patch.Members {
		path := cleanRelativePath(member.Path)
		switch member.Operation {
		case PatchWrite:
			content := []byte(member.Content)
			if member.ContentBase64 != "" {
				var err error
				content, err = base64.StdEncoding.DecodeString(member.ContentBase64)
				if err != nil {
					return nil, err
				}
			}
			after[path] = contentDigest(content)
		case PatchExact:
			before := contents[path]
			limit := 1
			if member.ReplaceAll {
				limit = -1
			}
			after[path] = contentDigest([]byte(strings.Replace(string(before.Content), member.OldText, member.NewText, limit)))
		case PatchJSONSet:
			content, err := applyJSONPointer(contents[path].Content, member.JSONPointer, member.JSONValue)
			if err != nil {
				return nil, err
			}
			after[path] = contentDigest(content)
		case PatchDelete:
			after[path] = absentHash
		case PatchRename:
			after[cleanRelativePath(member.Destination)] = after[path]
			after[path] = absentHash
		case PatchCopy:
			after[cleanRelativePath(member.Destination)] = after[path]
		case PatchMkdir:
			after[path] = directoryHash
		case PatchArchive:
			after[path] = member.ArchiveBaselineSHA256
		default:
			return nil, fmt.Errorf("project: unsupported patch operation %q", member.Operation)
		}
	}
	return after, nil
}

func snapshotDigest(snapshot patchSnapshot) string {
	if !snapshot.Exists {
		return absentHash
	}
	if snapshot.IsDir {
		return directoryHash
	}
	return contentDigest(snapshot.Content)
}

func contentDigest(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func restoreSnapshotsGuarded(root string, snapshots []patchSnapshot, expectedAfter map[string]string) error {
	var result error
	for index := len(snapshots) - 1; index >= 0; index-- {
		snapshot := snapshots[index]
		path, err := securePatchPath(root, snapshot.Path, true)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		current, _, _, currentErr := snapshotPath(path)
		if errors.Is(currentErr, os.ErrNotExist) {
			current, currentErr = absentHash, nil
		}
		if currentErr != nil {
			result = errors.Join(result, currentErr)
			continue
		}
		beforeDigest := snapshotDigest(snapshot)
		if current == beforeDigest {
			continue
		}
		if expectedAfter[snapshot.Path] == "" || current != expectedAfter[snapshot.Path] {
			result = errors.Join(result, PatchConflict{Path: snapshot.Path,
				Expected: expectedAfter[snapshot.Path], Actual: current})
			continue
		}
		if !snapshot.Exists {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				if removeDirErr := os.Remove(path); removeDirErr != nil && !errors.Is(removeDirErr, os.ErrNotExist) {
					result = errors.Join(result, removeDirErr)
				}
			}
			continue
		}
		if snapshot.IsDir {
			if err := os.Mkdir(path, snapshot.Mode.Perm()); err != nil && !errors.Is(err, os.ErrExist) {
				result = errors.Join(result, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			result = errors.Join(result, err)
			continue
		}
		result = errors.Join(result, writeProjectAtomic(path, snapshot.Content, snapshot.Mode))
	}
	return result
}

func buildPatchReceipt(root string, patch PatchSet, now time.Time) (PatchReceipt, error) {
	files := []PatchFileResult{}
	for _, member := range patch.Members {
		for _, relative := range []string{member.Path, member.Destination} {
			if relative == "" {
				continue
			}
			path, err := securePatchPath(root, relative, true)
			if err != nil {
				return PatchReceipt{}, err
			}
			after, _, _, err := snapshotPath(path)
			if errors.Is(err, os.ErrNotExist) {
				after, err = absentHash, nil
			}
			if err != nil {
				return PatchReceipt{}, err
			}
			before := member.ExpectedSHA256
			if relative == member.Destination {
				before = absentHash
			}
			files = append(files, PatchFileResult{Path: cleanRelativePath(relative),
				BeforeSHA256: before, AfterSHA256: after, Generated: member.Generated})
		}
	}
	classification, requiresApproval := classifyPatch(patch)
	return PatchReceipt{Version: PatchSetVersion, PatchSetID: patch.ID, ProjectID: patch.ProjectID,
		BaselineRevision: patch.BaselineRevision, Status: "applied", Criteria: append([]string(nil), patch.Criteria...),
		ValidationPlan: append([]string(nil), patch.ValidationPlan...), Files: files, AppliedAt: now,
		RollbackAvailable: true, Classification: classification, RequiresApproval: requiresApproval}, nil
}

func buildArchive(root string, sources []string) ([]byte, error) {
	type archiveFile struct {
		name string
		path string
		mode os.FileMode
	}
	files := map[string]archiveFile{}
	total := int64(0)
	for _, source := range sources {
		base, err := securePatchPath(root, source, false)
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(relative)
			if name == ".git" || strings.HasPrefix(name, ".git/") || entry.Type()&os.ModeSymlink != 0 ||
				protectedPathReason(name, nil) != "" || entry.IsDir() && defaultExcludedDirectory(entry.Name()) {
				return ErrProtectedPath
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return errors.Join(ErrProtectedPath, err)
			}
			total += info.Size()
			if total > maxPatchBytes || len(files) >= maxPatchMembers {
				return fmt.Errorf("project: archive exceeds bounded mutation limits")
			}
			files[name] = archiveFile{name: name, path: path, mode: info.Mode().Perm()}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var buffer bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&buffer, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	gzipWriter.ModTime = time.Unix(0, 0).UTC()
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range names {
		file := files[name]
		content, err := os.ReadFile(file.path)
		if err != nil {
			return nil, errors.Join(err, tarWriter.Close(), gzipWriter.Close())
		}
		header := &tar.Header{Name: file.name, Mode: int64(file.mode.Perm()), Size: int64(len(content)),
			ModTime: time.Unix(0, 0).UTC(), Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, errors.Join(err, tarWriter.Close(), gzipWriter.Close())
		}
		if _, err := tarWriter.Write(content); err != nil {
			return nil, errors.Join(err, tarWriter.Close(), gzipWriter.Close())
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, errors.Join(err, gzipWriter.Close())
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	if buffer.Len() > maxPatchBytes {
		return nil, fmt.Errorf("project: archive output exceeds bounded mutation limits")
	}
	return buffer.Bytes(), nil
}

func applyJSONPointer(document []byte, pointer string, rawValue json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("project: invalid JSON patch target: %w", err)
	}
	var value any
	valueDecoder := json.NewDecoder(bytes.NewReader(rawValue))
	valueDecoder.UseNumber()
	if err := valueDecoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("project: invalid JSON patch value: %w", err)
	}
	if pointer == "" {
		root = value
	} else {
		if !strings.HasPrefix(pointer, "/") {
			return nil, fmt.Errorf("project: invalid JSON pointer")
		}
		tokens := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
		for index := range tokens {
			decoded, err := decodeJSONPointerToken(tokens[index])
			if err != nil {
				return nil, err
			}
			tokens[index] = decoded
		}
		updated, err := setJSONValue(root, tokens, value)
		if err != nil {
			return nil, err
		}
		root = updated
	}
	result, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(result, '\n'), nil
}

func decodeJSONPointerToken(token string) (string, error) {
	for index := 0; index < len(token); index++ {
		if token[index] != '~' {
			continue
		}
		if index+1 >= len(token) || token[index+1] != '0' && token[index+1] != '1' {
			return "", fmt.Errorf("project: invalid JSON pointer escape")
		}
		index++
	}
	return strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~"), nil
}

func setJSONValue(node any, tokens []string, value any) (any, error) {
	if len(tokens) == 0 {
		return value, nil
	}
	switch current := node.(type) {
	case map[string]any:
		if len(tokens) == 1 {
			current[tokens[0]] = value
			return current, nil
		}
		child, exists := current[tokens[0]]
		if !exists {
			return nil, fmt.Errorf("project: JSON pointer parent does not exist")
		}
		updated, err := setJSONValue(child, tokens[1:], value)
		if err != nil {
			return nil, err
		}
		current[tokens[0]] = updated
		return current, nil
	case []any:
		index, err := strconv.Atoi(tokens[0])
		if err != nil || index < 0 || index >= len(current) {
			return nil, fmt.Errorf("project: JSON pointer array index is out of bounds")
		}
		updated, err := setJSONValue(current[index], tokens[1:], value)
		if err != nil {
			return nil, err
		}
		current[index] = updated
		return current, nil
	default:
		return nil, fmt.Errorf("project: JSON pointer crosses a scalar value")
	}
}

func securePatchPath(root, relative string, allowMissing bool) (string, error) {
	clean := cleanRelativePath(relative)
	if clean == "" || strings.HasPrefix(clean, ".git/") || clean == ".git" {
		return "", ErrProtectedPath
	}
	absolute := filepath.Join(root, filepath.FromSlash(clean))
	if !pathWithin(root, absolute) {
		return "", ErrProtectedPath
	}
	current := root
	parts := strings.Split(filepath.FromSlash(clean), string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && allowMissing {
			return absolute, nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || index < len(parts)-1 && !info.IsDir() {
			return "", ErrProtectedPath
		}
	}
	return absolute, nil
}

func writeProjectAtomic(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ion-patch-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return ErrProtectedPath
	}
	return os.Rename(name, path)
}

func (service *EditingService) save(ctx context.Context, actor, project uuid.UUID, journal patchJournal) error {
	raw, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(ctx, patchJournalKind, patchScope(actor, project), raw)
}

func (service *EditingService) load(ctx context.Context, actor, project uuid.UUID) (patchJournal, error) {
	raw, err := service.store.LoadLivingState(ctx, patchJournalKind, patchScope(actor, project))
	if err != nil {
		return patchJournal{}, err
	}
	var journal patchJournal
	if json.Unmarshal(raw, &journal) != nil || journal.ActorID != actor || journal.Patch.ProjectID != project {
		return patchJournal{}, fmt.Errorf("project: invalid encrypted patch journal")
	}
	return journal, nil
}

func (service *EditingService) record(ctx context.Context, actor, project uuid.UUID,
	receipt PatchReceipt, before []patchSnapshot) error {
	history, err := service.loadHistory(ctx, actor, project)
	if err != nil {
		return err
	}
	filtered := history.Records[:0]
	for index := range history.Records {
		if history.Records[index].Receipt.PatchSetID == receipt.PatchSetID {
			continue
		}
		history.Records[index].Before = nil
		history.Records[index].Receipt.RollbackAvailable = false
		filtered = append(filtered, history.Records[index])
	}
	history.Records = filtered
	history.Records = append(history.Records, patchRecord{Receipt: receipt, Before: before})
	if len(history.Records) > 64 {
		history.Records = history.Records[len(history.Records)-64:]
	}
	raw, err := json.Marshal(history)
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(ctx, patchHistoryKind, patchScope(actor, project), raw)
}

func (service *EditingService) loadHistory(ctx context.Context, actor, project uuid.UUID) (patchHistory, error) {
	history := patchHistory{Records: []patchRecord{}}
	raw, err := service.store.LoadLivingState(ctx, patchHistoryKind, patchScope(actor, project))
	if errors.Is(err, sql.ErrNoRows) {
		return history, nil
	}
	if err != nil {
		return patchHistory{}, err
	}
	if json.Unmarshal(raw, &history) != nil {
		return patchHistory{}, fmt.Errorf("project: invalid encrypted patch history")
	}
	return history, nil
}

func (service *EditingService) History(ctx context.Context, actor, projectID uuid.UUID) ([]PatchReceipt, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if _, err := service.projects.Get(ctx, actor, projectID); err != nil {
		return nil, err
	}
	history, err := service.loadHistory(ctx, actor, projectID)
	if err != nil {
		return nil, err
	}
	receipts := make([]PatchReceipt, 0, len(history.Records))
	for _, record := range history.Records {
		receipts = append(receipts, record.Receipt)
	}
	return receipts, nil
}

func (service *EditingService) Rollback(ctx context.Context, actor uuid.UUID, request PatchRollbackRequest) (PatchReceipt, error) {
	service.mu.Lock()
	if actor == uuid.Nil || request.ProjectID == uuid.Nil || request.PatchSetID == uuid.Nil || request.WorkspaceRevision == 0 {
		service.mu.Unlock()
		return PatchReceipt{}, fmt.Errorf("project: complete rollback request is required")
	}
	project, err := service.projects.Get(ctx, actor, request.ProjectID)
	if err != nil {
		service.mu.Unlock()
		return PatchReceipt{}, err
	}
	if project.WorkspaceRevision != request.WorkspaceRevision {
		service.mu.Unlock()
		return PatchReceipt{}, ErrStaleRevision
	}
	history, err := service.loadHistory(ctx, actor, request.ProjectID)
	if err != nil || len(history.Records) == 0 {
		service.mu.Unlock()
		if err != nil {
			return PatchReceipt{}, err
		}
		return PatchReceipt{}, ErrNotFound
	}
	record := history.Records[len(history.Records)-1]
	service.mu.Unlock()
	if record.Receipt.PatchSetID != request.PatchSetID || !record.Receipt.RollbackAvailable || len(record.Before) == 0 {
		return PatchReceipt{}, ErrConflict
	}
	after := map[string]string{}
	for _, file := range record.Receipt.Files {
		after[cleanRelativePath(file.Path)] = file.AfterSHA256
	}
	members := make([]PatchMember, 0, len(record.Before))
	for _, snapshot := range record.Before {
		expected, ok := after[snapshot.Path]
		if !ok {
			return PatchReceipt{}, fmt.Errorf("project: rollback receipt is incomplete")
		}
		if snapshot.Exists {
			if snapshot.IsDir {
				if expected != directoryHash {
					members = append(members, PatchMember{Operation: PatchMkdir, Path: snapshot.Path, ExpectedSHA256: expected})
				}
				continue
			}
			digest := sha256.Sum256(snapshot.Content)
			if expected == hex.EncodeToString(digest[:]) {
				continue
			}
			members = append(members, PatchMember{Operation: PatchWrite, Path: snapshot.Path,
				ExpectedSHA256: expected, ContentBase64: base64.StdEncoding.EncodeToString(snapshot.Content)})
		} else if expected != absentHash {
			members = append(members, PatchMember{Operation: PatchDelete, Path: snapshot.Path, ExpectedSHA256: expected})
		}
	}
	if len(members) == 0 {
		return PatchReceipt{}, ErrConflict
	}
	return service.ApplyApproved(ctx, actor, PatchSet{Version: PatchSetVersion, ID: uuid.New(),
		ProjectID: request.ProjectID, BaselineRevision: request.WorkspaceRevision,
		Criteria:       []string{"restore the exact preimage of patch " + request.PatchSetID.String()},
		ValidationPlan: []string{"verify every restored path against its recorded preimage"}, Members: members})
}

func patchScope(actor, project uuid.UUID) string { return actor.String() + ":" + project.String() }

func (service *Service) ApplyPatchSet(ctx context.Context, actor uuid.UUID, patch PatchSet) (PatchReceipt, error) {
	return service.editing.Apply(ctx, actor, patch)
}

func (service *Service) ApplyPatchSetApproved(ctx context.Context, actor uuid.UUID, patch PatchSet,
	approved bool) (PatchReceipt, error) {
	if approved {
		return service.editing.ApplyApproved(ctx, actor, patch)
	}
	return service.editing.Apply(ctx, actor, patch)
}

func (service *Service) PatchHistory(ctx context.Context, actor, projectID uuid.UUID) ([]PatchReceipt, error) {
	return service.editing.History(ctx, actor, projectID)
}

func (service *Service) RollbackPatchSet(ctx context.Context, actor uuid.UUID, request PatchRollbackRequest) (PatchReceipt, error) {
	return service.editing.Rollback(ctx, actor, request)
}

func (service *Service) bumpWorkspaceRevision(ctx context.Context, actor, projectID uuid.UUID,
	expected uint64) (Project, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor)
	if err != nil {
		return Project{}, err
	}
	project, index, ok := findProject(state.Projects, projectID)
	if !ok {
		return Project{}, ErrNotFound
	}
	if project.WorkspaceRevision != expected {
		return Project{}, ErrStaleRevision
	}
	project.WorkspaceRevision++
	project.UpdatedAt = service.clock.Now().UTC()
	state.Projects[index] = project
	if err := service.save(ctx, actor, &state); err != nil {
		return Project{}, err
	}
	return project, nil
}
