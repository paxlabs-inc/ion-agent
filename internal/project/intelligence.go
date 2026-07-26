package project

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	intelligenceStateKind = "project_intelligence_v1"
	maxPersistIndexBytes  = 900 << 10
)

// Intelligence owns the bounded, encrypted structural index for projects in
// the registry. It never trusts a caller-supplied root and never persists file
// content or discovered secret values.
type Intelligence struct {
	mu       sync.Mutex
	store    *session.Store
	clock    types.Clock
	projects *Service
}

func newIntelligence(store *session.Store, clock types.Clock, projects *Service) *Intelligence {
	return &Intelligence{store: store, clock: clock, projects: projects}
}

func (intelligence *Intelligence) Refresh(ctx context.Context, actor uuid.UUID, input RefreshInput) (ProjectIndex, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.WorkspaceRevision == 0 {
		return ProjectIndex{}, fmt.Errorf("project: actor, project, and workspace revision are required")
	}
	intelligence.mu.Lock()
	defer intelligence.mu.Unlock()
	project, err := intelligence.projects.Get(ctx, actor, input.ProjectID)
	if err != nil {
		return ProjectIndex{}, err
	}
	if project.WorkspaceRevision != input.WorkspaceRevision {
		return ProjectIndex{}, ErrStaleRevision
	}
	prior, err := intelligence.load(ctx, actor, input.ProjectID)
	if err != nil && !errors.Is(err, ErrIndexNotFound) {
		return ProjectIndex{}, err
	}
	index, err := buildProjectIndex(ctx, actor, project, prior, input, intelligence.clock.Now().UTC())
	if err != nil {
		return ProjectIndex{}, err
	}
	index = compactIndex(index)
	encoded, err := json.Marshal(index)
	if err != nil {
		return ProjectIndex{}, err
	}
	if len(encoded) > maxPersistIndexBytes {
		return ProjectIndex{}, fmt.Errorf("project: bounded intelligence index exceeds encrypted state limit")
	}
	if err := intelligence.store.SaveLivingState(ctx, intelligenceStateKind,
		intelligenceScope(actor, input.ProjectID), encoded); err != nil {
		return ProjectIndex{}, err
	}
	return cloneIndex(index), nil
}

func (intelligence *Intelligence) Get(ctx context.Context, actor, projectID uuid.UUID) (ProjectIndex, error) {
	if actor == uuid.Nil || projectID == uuid.Nil {
		return ProjectIndex{}, fmt.Errorf("project: actor and project are required")
	}
	intelligence.mu.Lock()
	defer intelligence.mu.Unlock()
	project, err := intelligence.projects.Get(ctx, actor, projectID)
	if err != nil {
		return ProjectIndex{}, err
	}
	index, err := intelligence.load(ctx, actor, projectID)
	if err != nil {
		return ProjectIndex{}, err
	}
	if index.WorkspaceRevision != project.WorkspaceRevision {
		return ProjectIndex{}, ErrStaleIndex
	}
	return cloneIndex(index), nil
}

func (intelligence *Intelligence) Search(ctx context.Context, actor uuid.UUID, request SearchRequest) (SearchResponse, error) {
	intelligence.mu.Lock()
	defer intelligence.mu.Unlock()
	project, index, err := intelligence.current(ctx, actor, request.ProjectID,
		request.WorkspaceRevision, request.ExpectedIndexRevision)
	if err != nil {
		return SearchResponse{}, err
	}
	return searchProject(ctx, project, index, request)
}

func (intelligence *Intelligence) Plan(ctx context.Context, actor uuid.UUID, request ContextPlanRequest) (ContextPack, error) {
	intelligence.mu.Lock()
	defer intelligence.mu.Unlock()
	project, index, err := intelligence.current(ctx, actor, request.ProjectID,
		request.WorkspaceRevision, request.ExpectedIndexRevision)
	if err != nil {
		return ContextPack{}, err
	}
	return planProjectContext(ctx, project, index, request, intelligence.clock.Now().UTC())
}

func (intelligence *Intelligence) ValidateCitation(ctx context.Context, actor uuid.UUID, citation Citation) error {
	intelligence.mu.Lock()
	defer intelligence.mu.Unlock()
	project, index, err := intelligence.current(ctx, actor, citation.ProjectID,
		citation.WorkspaceRevision, 0)
	if err != nil {
		return err
	}
	if citation.IndexRevision == 0 || citation.IndexRevision != index.IndexRevision {
		return ErrStaleCitation
	}
	path := cleanRelativePath(citation.Path)
	if path == "" || path != citation.Path {
		return ErrStaleCitation
	}
	record, ok := fileByPath(index.Files, path)
	if !ok || record.Class != ContentSource && record.Class != ContentGenerated {
		return ErrProtectedPath
	}
	if record.SHA256 == "" || record.SHA256 != citation.SHA256 {
		return ErrStaleCitation
	}
	absolute := filepath.Join(project.Root, filepath.FromSlash(path))
	if !pathWithin(project.Root, absolute) {
		return ErrProtectedPath
	}
	digest, err := intelligenceDigestFile(absolute)
	if err != nil || digest != citation.SHA256 {
		return ErrStaleCitation
	}
	return nil
}

func (intelligence *Intelligence) current(ctx context.Context, actor, projectID uuid.UUID,
	workspaceRevision, indexRevision uint64) (Project, ProjectIndex, error) {
	if actor == uuid.Nil || projectID == uuid.Nil || workspaceRevision == 0 {
		return Project{}, ProjectIndex{}, fmt.Errorf("project: actor, project, and workspace revision are required")
	}
	project, err := intelligence.projects.Get(ctx, actor, projectID)
	if err != nil {
		return Project{}, ProjectIndex{}, err
	}
	if project.WorkspaceRevision != workspaceRevision {
		return Project{}, ProjectIndex{}, ErrStaleRevision
	}
	index, err := intelligence.load(ctx, actor, projectID)
	if err != nil {
		return Project{}, ProjectIndex{}, err
	}
	if index.WorkspaceRevision != workspaceRevision || index.ProjectID != projectID ||
		index.ActorID != actor || index.Version != IntelligenceVersion {
		return Project{}, ProjectIndex{}, ErrStaleIndex
	}
	if indexRevision != 0 && index.IndexRevision != indexRevision {
		return Project{}, ProjectIndex{}, ErrStaleIndex
	}
	return project, index, nil
}

func (intelligence *Intelligence) load(ctx context.Context, actor, projectID uuid.UUID) (ProjectIndex, error) {
	raw, err := intelligence.store.LoadLivingState(ctx, intelligenceStateKind,
		intelligenceScope(actor, projectID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProjectIndex{}, ErrIndexNotFound
		}
		return ProjectIndex{}, err
	}
	var index ProjectIndex
	if err := json.Unmarshal(raw, &index); err != nil || index.Version != IntelligenceVersion ||
		index.ActorID != actor || index.ProjectID != projectID {
		return ProjectIndex{}, fmt.Errorf("project: invalid encrypted intelligence index")
	}
	return index, nil
}

func intelligenceScope(actor, projectID uuid.UUID) string {
	return actor.String() + ":" + projectID.String()
}

func compactIndex(index ProjectIndex) ProjectIndex {
	encoded, _ := json.Marshal(index)
	if len(encoded) <= maxPersistIndexBytes {
		return index
	}
	for fileIndex := len(index.Files) - 1; fileIndex >= 0 && len(encoded) > maxPersistIndexBytes; fileIndex-- {
		index.Files[fileIndex].References = nil
		encoded, _ = json.Marshal(index)
	}
	for fileIndex := len(index.Files) - 1; fileIndex >= 0 && len(encoded) > maxPersistIndexBytes; fileIndex-- {
		index.Files[fileIndex].Symbols = nil
		index.Files[fileIndex].Dependencies = nil
		encoded, _ = json.Marshal(index)
	}
	for len(index.Files) > 1 && len(encoded) > maxPersistIndexBytes {
		removed := len(index.Files) / 8
		if removed < 1 {
			removed = 1
		}
		index.Files = index.Files[:len(index.Files)-removed]
		index.Truncated = true
		index.Omissions = append(index.Omissions, Omission{Class: "index_limit",
			Reason: "file metadata omitted to remain within encrypted index bounds", Count: removed})
		encoded, _ = json.Marshal(index)
	}
	return index
}

func cloneIndex(index ProjectIndex) ProjectIndex {
	raw, _ := json.Marshal(index)
	var cloned ProjectIndex
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func fileByPath(files []FileRecord, path string) (FileRecord, bool) {
	index := sort.Search(len(files), func(index int) bool { return files[index].Path >= path })
	if index < len(files) && files[index].Path == path {
		return files[index], true
	}
	return FileRecord{}, false
}

func cleanRelativePath(path string) string {
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if path == "." || path == "" || path == ".." || strings.HasPrefix(path, "../") || filepath.IsAbs(path) {
		return ""
	}
	return path
}

func intelligenceDigestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// RefreshIndex rebuilds project intelligence within the actor-scoped project
// composition root.
func (service *Service) RefreshIndex(ctx context.Context, actor uuid.UUID, input RefreshInput) (ProjectIndex, error) {
	return service.intelligence.Refresh(ctx, actor, input)
}

func (service *Service) ProjectIndex(ctx context.Context, actor, projectID uuid.UUID) (ProjectIndex, error) {
	index, err := service.intelligence.Get(ctx, actor, projectID)
	if err == nil {
		return index, nil
	}
	if !errors.Is(err, ErrIndexNotFound) && !errors.Is(err, ErrStaleIndex) {
		return ProjectIndex{}, err
	}
	project, projectErr := service.Get(ctx, actor, projectID)
	if projectErr != nil {
		return ProjectIndex{}, projectErr
	}
	return service.intelligence.Refresh(ctx, actor, RefreshInput{
		ProjectID: projectID, WorkspaceRevision: project.WorkspaceRevision,
	})
}

func (service *Service) SearchProject(ctx context.Context, actor uuid.UUID, request SearchRequest) (SearchResponse, error) {
	return service.intelligence.Search(ctx, actor, request)
}

func (service *Service) PlanProjectContext(ctx context.Context, actor uuid.UUID, request ContextPlanRequest) (ContextPack, error) {
	return service.intelligence.Plan(ctx, actor, request)
}

func (service *Service) VerifyProjectCitation(ctx context.Context, actor uuid.UUID, citation Citation) error {
	return service.intelligence.ValidateCitation(ctx, actor, citation)
}
