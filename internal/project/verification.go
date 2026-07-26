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
	"regexp"
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
	verificationStateKind = "project_verification_v1"
	maxVerificationRuns   = 64
	maxVerificationLog    = 64 << 10
)

var verificationIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{0,95}$`)
var verificationVolatilePattern = regexp.MustCompile(`(?i)(/tmp/[^\s]+|[0-9a-f]{8}-[0-9a-f-]{27,}|0x[0-9a-f]+|\b\d{2,}\b)`)

type verificationState struct {
	Revision uint64                `json:"revision"`
	Manifest *VerificationManifest `json:"manifest,omitempty"`
	Runs     []VerificationRun     `json:"runs"`
	Waivers  []VerificationWaiver  `json:"waivers"`
}

type VerificationService struct {
	mu       sync.Mutex
	store    *session.Store
	clock    types.Clock
	projects *Service
}

func newVerificationService(store *session.Store, clock types.Clock, projects *Service) *VerificationService {
	return &VerificationService{store: store, clock: clock, projects: projects}
}

func (service *VerificationService) Derive(ctx context.Context, actor uuid.UUID,
	input VerificationManifestInput) (VerificationManifest, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.WorkspaceRevision == 0 ||
		len(input.Criteria) == 0 || len(input.Criteria) > 128 {
		return VerificationManifest{}, fmt.Errorf("project: bounded verification criteria are required")
	}
	project, err := service.projects.Get(ctx, actor, input.ProjectID)
	if err != nil {
		return VerificationManifest{}, err
	}
	if project.WorkspaceRevision != input.WorkspaceRevision {
		return VerificationManifest{}, ErrStaleRevision
	}
	criteria, err := normalizeVerificationCriteria(input.Criteria)
	if err != nil {
		return VerificationManifest{}, err
	}
	gates := append([]VerificationGate(nil), input.Gates...)
	if len(gates) == 0 {
		gates, err = service.deriveGates(project, criteria)
		if err != nil {
			return VerificationManifest{}, err
		}
	}
	if err := validateVerificationGates(gates, criteria); err != nil {
		return VerificationManifest{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, input.ProjectID)
	if err != nil {
		return VerificationManifest{}, err
	}
	state.Revision++
	var inducingPatchID *uuid.UUID
	patches, err := service.projects.PatchHistory(ctx, actor, project.ID)
	if err != nil {
		return VerificationManifest{}, err
	}
	for index := len(patches) - 1; index >= 0; index-- {
		if patches[index].WorkspaceRevision == project.WorkspaceRevision {
			patchID := patches[index].PatchSetID
			inducingPatchID = &patchID
			break
		}
	}
	manifest := VerificationManifest{Version: VerificationContractVersion, ID: uuid.New(),
		ActorID: actor, ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		InducingPatchID: inducingPatchID, Revision: state.Revision, Criteria: criteria,
		Gates: gates, CreatedAt: service.clock.Now().UTC()}
	state.Manifest = &manifest
	if err := service.save(ctx, actor, input.ProjectID, state); err != nil {
		return VerificationManifest{}, err
	}
	return manifest, nil
}

func (service *VerificationService) deriveGates(project Project,
	criteria []VerificationCriterion) ([]VerificationGate, error) {
	gates := []VerificationGate{}
	add := func(id, kind string, argv []string, timeout int, evidence ...string) {
		gates = append(gates, VerificationGate{ID: id, Kind: kind, Argv: argv,
			Environment: []string{"CI=1"}, TimeoutSeconds: timeout,
			Required: true, EvidenceKinds: evidence, Available: true})
	}
	manifestPath := filepath.Join(project.Root, "package.json")
	if raw, err := os.ReadFile(manifestPath); err == nil {
		var manifest struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(raw, &manifest) != nil {
			return nil, fmt.Errorf("project: package.json is invalid")
		}
		manager := "npm"
		if regularFile(filepath.Join(project.Root, "pnpm-lock.yaml")) {
			manager = "pnpm"
		} else if regularFile(filepath.Join(project.Root, "yarn.lock")) {
			manager = "yarn"
		}
		for _, candidate := range []struct {
			script   string
			kind     string
			timeout  int
			evidence []string
		}{
			{"format", "format", 300, []string{"logs"}},
			{"format:check", "format", 300, []string{"logs"}},
			{"lint", "lint", 600, []string{"logs"}},
			{"typecheck", "type", 600, []string{"logs"}},
			{"build", "compile", 900, []string{"logs"}},
			{"test", "test", 900, []string{"logs"}},
			{"test:unit", "unit", 900, []string{"logs"}},
			{"test:integration", "integration", 1200, []string{"logs"}},
			{"test:e2e", "end-to-end", 1200, []string{"logs"}},
			{"test:a11y", "accessibility", 900, []string{"logs"}},
			{"test:security", "security", 900, []string{"logs"}},
			{"audit", "dependency", 900, []string{"logs"}},
			{"test:dependency", "dependency", 900, []string{"logs"}},
			{"check:dependencies", "dependency", 900, []string{"logs"}},
			{"license", "license", 900, []string{"logs"}},
			{"test:license", "license", 900, []string{"logs"}},
			{"check:licenses", "license", 900, []string{"logs"}},
			{"test:migration", "migration", 900, []string{"logs"}},
			{"test:performance", "performance", 1200, []string{"logs"}},
			{"package", "package", 1200, []string{"logs"}},
		} {
			if _, ok := manifest.Scripts[candidate.script]; ok {
				add(strings.ReplaceAll(candidate.script, ":", "-"), candidate.kind,
					[]string{manager, "run", candidate.script}, candidate.timeout, candidate.evidence...)
			}
		}
	} else if regularFile(filepath.Join(project.Root, "go.mod")) {
		add("format", "format", []string{"go", "fmt", "./..."}, 300, "logs")
		add("vet", "lint", []string{"go", "vet", "./..."}, 900, "logs", "report")
		add("build", "compile", []string{"go", "build", "./..."}, 900, "logs")
		add("test", "test", []string{"go", "test", "./..."}, 1200, "logs")
	}
	requiredKinds := map[string]bool{}
	for _, criterion := range criteria {
		for _, kind := range criterion.Kinds {
			requiredKinds[kind] = true
		}
	}
	for kind := range requiredKinds {
		found := false
		for index := range gates {
			if gates[index].Kind == kind {
				found = true
			}
		}
		if !found {
			gates = append(gates, VerificationGate{ID: "unavailable-" + kind, Kind: kind,
				TimeoutSeconds: 1, Required: true, EvidenceKinds: []string{"unavailable"},
				Available: false, UnavailableReason: "No inspected repository command provides this required gate."})
		}
	}
	if len(gates) == 0 {
		gates = append(gates, VerificationGate{ID: "unavailable-test", Kind: "test",
			TimeoutSeconds: 1, Required: true, EvidenceKinds: []string{"unavailable"},
			Available: false, UnavailableReason: "No safe verification command was discovered."})
	}
	for index := range gates {
		for _, criterion := range criteria {
			if len(criterion.Kinds) == 0 || containsVerificationString(criterion.Kinds, gates[index].Kind) {
				gates[index].Criteria = append(gates[index].Criteria, criterion.ID)
			}
		}
	}
	sort.Slice(gates, func(i, j int) bool { return gates[i].ID < gates[j].ID })
	return gates, nil
}

func (service *VerificationService) Current(ctx context.Context, actor,
	projectID uuid.UUID) (VerificationManifest, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, projectID)
	if err != nil {
		return VerificationManifest{}, err
	}
	if state.Manifest == nil {
		return VerificationManifest{}, ErrNotFound
	}
	return *state.Manifest, nil
}

func (service *VerificationService) Run(ctx context.Context, actor uuid.UUID,
	request VerificationRunRequest) (VerificationRun, error) {
	if actor == uuid.Nil || request.ProjectID == uuid.Nil || request.ManifestID == uuid.Nil {
		return VerificationRun{}, fmt.Errorf("project: valid verification run request is required")
	}
	if request.MaxAttempts == 0 {
		request.MaxAttempts = 3
	}
	if request.MaxAttempts < 1 || request.MaxAttempts > 10 {
		return VerificationRun{}, fmt.Errorf("project: verification repair budget is out of bounds")
	}
	service.mu.Lock()
	state, err := service.load(ctx, actor, request.ProjectID)
	if err != nil {
		service.mu.Unlock()
		return VerificationRun{}, err
	}
	if state.Manifest == nil || state.Manifest.ID != request.ManifestID {
		service.mu.Unlock()
		return VerificationRun{}, fmt.Errorf("project: verification manifest not found")
	}
	manifest := *state.Manifest
	waivers := append([]VerificationWaiver(nil), state.Waivers...)
	history := append([]VerificationRun(nil), state.Runs...)
	service.mu.Unlock()
	project, err := service.projects.Get(ctx, actor, request.ProjectID)
	if err != nil {
		return VerificationRun{}, err
	}
	if project.WorkspaceRevision != manifest.WorkspaceRevision {
		return VerificationRun{}, ErrStaleRevision
	}
	selected, mode, err := selectVerificationGates(manifest.Gates, request.GateIDs, request.Full)
	if err != nil {
		return VerificationRun{}, err
	}
	run := VerificationRun{Version: VerificationContractVersion, ID: uuid.New(), ActorID: actor,
		ProjectID: project.ID, ManifestID: manifest.ID, ManifestRevision: manifest.Revision,
		WorkspaceRevision: project.WorkspaceRevision, InducingPatchID: manifest.InducingPatchID,
		Mode: mode, Status: "running",
		Results: []VerificationGateResult{}, StartedAt: service.clock.Now().UTC()}
	for _, gate := range selected {
		result := service.runGate(ctx, actor, project, manifest, gate, waivers)
		run.Results = append(run.Results, result)
		if ctx.Err() != nil {
			break
		}
	}
	run.FinishedAt = service.clock.Now().UTC()
	run.CriteriaCovered, run.UncoveredCriteria = verificationCoverage(manifest, run.Results)
	run.Status = verificationRunStatus(run, manifest, request.Full)
	run.Repair = evaluateVerificationRepair(run, history, request.MaxAttempts)
	if run.Repair.State == "stop_flaky" {
		run.Status = "flaky"
		run.CriteriaCovered = nil
		run.UncoveredCriteria = verificationCriterionIDs(manifest.Criteria)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err = service.load(ctx, actor, request.ProjectID)
	if err != nil {
		return VerificationRun{}, err
	}
	if state.Manifest == nil || state.Manifest.ID != manifest.ID ||
		state.Manifest.Revision != manifest.Revision {
		return VerificationRun{}, ErrStaleRevision
	}
	state.Runs = append(state.Runs, run)
	if len(state.Runs) > maxVerificationRuns {
		state.Runs = append([]VerificationRun(nil), state.Runs[len(state.Runs)-maxVerificationRuns:]...)
	}
	if err := service.save(ctx, actor, request.ProjectID, state); err != nil {
		return VerificationRun{}, err
	}
	return run, nil
}

func (service *VerificationService) runGate(ctx context.Context, actor uuid.UUID, project Project,
	manifest VerificationManifest, gate VerificationGate, waivers []VerificationWaiver) VerificationGateResult {
	result := VerificationGateResult{GateID: gate.ID, Kind: gate.Kind,
		Status: "unavailable", CriteriaCovered: []string{}, Evidence: []VerificationEvidence{}}
	if waiver := activeVerificationWaiver(waivers, manifest.ID, gate.ID, service.clock.Now().UTC()); waiver != nil {
		result.Status, result.WaiverID = "waived", &waiver.ID
		result.UnavailableReason = "Explicit waiver leaves this gate and its criteria uncovered."
		return result
	}
	if !gate.Available {
		result.UnavailableReason = gate.UnavailableReason
		result.FailureSignature = verificationFailureSignature(gate, result.Status, nil, result.UnavailableReason)
		return result
	}
	started := service.clock.Now().UTC()
	argv := append([]string(nil), gate.Argv...)
	if len(argv) > 0 && strings.ContainsRune(argv[0], filepath.Separator) && !filepath.IsAbs(argv[0]) {
		directory, resolveErr := secureWorkspaceDirectory(project.Root, gate.WorkingDirectory)
		if resolveErr != nil {
			result.Status, result.UnavailableReason = "environment", resolveErr.Error()
			result.FailureSignature = verificationFailureSignature(gate, result.Status, nil, resolveErr.Error())
			return result
		}
		executable, resolveErr := securePatchPath(directory, argv[0], false)
		if resolveErr != nil {
			result.Status, result.UnavailableReason = "environment", resolveErr.Error()
			result.FailureSignature = verificationFailureSignature(gate, result.Status, nil, resolveErr.Error())
			return result
		}
		argv[0] = executable
	}
	terminal, err := service.projects.terminals.Start(ctx, actor, ProcessRequest{ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, Mode: ProcessOneShot, Argv: argv,
		WorkingDirectory: gate.WorkingDirectory, Environment: verificationEnvironmentOverrides(gate.Environment),
		TimeoutSeconds: gate.TimeoutSeconds, OutputBytes: 4 << 20})
	if err != nil {
		result.Status, result.UnavailableReason = "environment", err.Error()
		result.FailureSignature = verificationFailureSignature(gate, result.Status, nil, err.Error())
		return result
	}
	result.TerminalID = &terminal.ID
	var replay TerminalReplay
	for {
		replay, err = service.projects.terminals.Replay(ctx, actor, terminal.ID, 0)
		if err != nil {
			result.Status, result.UnavailableReason = "environment", err.Error()
			break
		}
		if replay.State.Status != "running" && replay.State.Status != "starting" {
			break
		}
		select {
		case <-ctx.Done():
			_ = service.projects.terminals.cancelRuntime(context.Background(), actor, terminal.ID)
			result.Status, result.UnavailableReason = "cancelled", ctx.Err().Error()
			break
		case <-time.After(20 * time.Millisecond):
		}
		if result.Status == "cancelled" {
			break
		}
	}
	result.ExitCode = replay.State.ExitCode
	result.DurationMillis = service.clock.Now().UTC().Sub(started).Milliseconds()
	result.Logs = replay.Output
	if len(result.Logs) > maxVerificationLog {
		result.Logs = result.Logs[len(result.Logs)-maxVerificationLog:]
		result.LogsTruncated = true
	}
	result.LogsTruncated = result.LogsTruncated || replay.State.Truncated || replay.Gap
	switch {
	case result.Status == "cancelled" || result.Status == "environment":
	case replay.State.TimedOut || replay.State.Status == "timed_out":
		result.Status = "timeout"
	case replay.State.Status == "completed" && replay.State.ExitCode != nil && *replay.State.ExitCode == 0:
		result.Status = "passed"
		result.Evidence = service.collectVerificationEvidence(project.Root, gate, result.Logs)
		if verificationEvidenceSatisfied(gate.EvidenceKinds, result.Evidence) {
			result.CriteriaCovered = append([]string(nil), gate.Criteria...)
		} else {
			result.Status = "failed"
			result.UnavailableReason = "Required verification evidence was not produced by the successful command."
		}
	default:
		result.Status = "failed"
	}
	if result.Status != "passed" {
		result.FailureSignature = verificationFailureSignature(gate, result.Status, result.ExitCode, result.Logs+result.UnavailableReason)
	}
	return result
}

func (service *VerificationService) collectVerificationEvidence(root string,
	gate VerificationGate, logs string) []VerificationEvidence {
	logDigest := sha256.Sum256([]byte(logs))
	result := []VerificationEvidence{{Kind: "logs", Reference: "terminal",
		SHA256: hex.EncodeToString(logDigest[:]), SizeBytes: int64(len(logs))}}
	for _, path := range gate.EvidencePaths {
		resolved, err := securePatchPath(root, path, false)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
			continue
		}
		hash, _, _, err := snapshotPath(resolved)
		if err != nil {
			continue
		}
		result = append(result, VerificationEvidence{Kind: verificationEvidenceKind(path),
			Reference: cleanRelativePath(path), SHA256: hash, SizeBytes: info.Size()})
	}
	return result
}

func verificationEvidenceSatisfied(required []string, evidence []VerificationEvidence) bool {
	found := map[string]bool{}
	for _, item := range evidence {
		found[item.Kind] = true
		if item.Kind == "artifact" {
			found["artifact_hash"] = true
		}
	}
	for _, kind := range required {
		if !found[kind] {
			return false
		}
	}
	return true
}

func (service *VerificationService) PutWaiver(ctx context.Context, actor uuid.UUID,
	input VerificationWaiverInput) (VerificationWaiver, error) {
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.ManifestID == uuid.Nil ||
		len(input.GateIDs) == 0 || len(input.Criteria) == 0 ||
		strings.TrimSpace(input.Reason) == "" || strings.TrimSpace(input.Risk) == "" ||
		!input.ExpiresAt.After(service.clock.Now().UTC()) ||
		input.ExpiresAt.After(service.clock.Now().UTC().Add(90*24*time.Hour)) {
		return VerificationWaiver{}, fmt.Errorf("project: scoped expiring verification waiver is required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, input.ProjectID)
	if err != nil {
		return VerificationWaiver{}, err
	}
	if state.Manifest == nil || state.Manifest.ID != input.ManifestID ||
		!verificationGateSubset(state.Manifest.Gates, input.GateIDs) ||
		!verificationCriteriaSubset(state.Manifest.Criteria, input.Criteria) {
		return VerificationWaiver{}, fmt.Errorf("project: waiver scope is outside the current manifest")
	}
	waiver := VerificationWaiver{ID: uuid.New(), ActorID: actor, ProjectID: input.ProjectID,
		ManifestID: input.ManifestID, GateIDs: trimVerificationStrings(input.GateIDs),
		Criteria: trimVerificationStrings(input.Criteria), Reason: strings.TrimSpace(input.Reason),
		Risk: strings.TrimSpace(input.Risk), ExpiresAt: input.ExpiresAt.UTC(),
		CreatedAt: service.clock.Now().UTC()}
	state.Waivers = append(state.Waivers, waiver)
	if err := service.save(ctx, actor, input.ProjectID, state); err != nil {
		return VerificationWaiver{}, err
	}
	return waiver, nil
}

func (service *VerificationService) ListRuns(ctx context.Context, actor,
	projectID uuid.UUID) ([]VerificationRun, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, projectID)
	if err != nil {
		return nil, err
	}
	return append([]VerificationRun(nil), state.Runs...), nil
}

func (service *VerificationService) ListWaivers(ctx context.Context, actor,
	projectID uuid.UUID) ([]VerificationWaiver, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor, projectID)
	if err != nil {
		return nil, err
	}
	return append([]VerificationWaiver(nil), state.Waivers...), nil
}

func (service *VerificationService) load(ctx context.Context, actor,
	projectID uuid.UUID) (verificationState, error) {
	if actor == uuid.Nil || projectID == uuid.Nil {
		return verificationState{}, fmt.Errorf("project: actor and project are required")
	}
	raw, err := service.store.LoadLivingState(ctx, verificationStateKind, patchScope(actor, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return verificationState{Runs: []VerificationRun{}, Waivers: []VerificationWaiver{}}, nil
	}
	if err != nil {
		return verificationState{}, err
	}
	var state verificationState
	if json.Unmarshal(raw, &state) != nil {
		return verificationState{}, fmt.Errorf("project: invalid encrypted verification state")
	}
	return state, nil
}

func (service *VerificationService) save(ctx context.Context, actor, projectID uuid.UUID,
	state verificationState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(ctx, verificationStateKind, patchScope(actor, projectID), raw)
}

func normalizeVerificationCriteria(input []VerificationCriterion) ([]VerificationCriterion, error) {
	result := make([]VerificationCriterion, 0, len(input))
	seen := map[string]bool{}
	for _, criterion := range input {
		criterion.ID = strings.TrimSpace(criterion.ID)
		criterion.Description = strings.TrimSpace(criterion.Description)
		criterion.Kinds = trimVerificationStrings(criterion.Kinds)
		if !verificationIDPattern.MatchString(criterion.ID) || criterion.Description == "" ||
			len(criterion.Description) > 2000 || seen[criterion.ID] {
			return nil, fmt.Errorf("project: verification criterion is invalid or duplicated")
		}
		for _, kind := range criterion.Kinds {
			if !verificationIDPattern.MatchString(kind) {
				return nil, fmt.Errorf("project: verification criterion kind is invalid")
			}
		}
		seen[criterion.ID] = true
		result = append(result, criterion)
	}
	return result, nil
}

func validateVerificationGates(gates []VerificationGate, criteria []VerificationCriterion) error {
	if len(gates) == 0 || len(gates) > 64 {
		return fmt.Errorf("project: bounded verification gates are required")
	}
	criterionIDs := map[string]bool{}
	for _, criterion := range criteria {
		criterionIDs[criterion.ID] = true
	}
	seen := map[string]bool{}
	for index := range gates {
		gate := &gates[index]
		gate.ID, gate.Kind = strings.TrimSpace(gate.ID), strings.TrimSpace(gate.Kind)
		gate.Criteria, gate.EvidenceKinds, gate.EvidencePaths =
			trimVerificationStrings(gate.Criteria), trimVerificationStrings(gate.EvidenceKinds),
			trimVerificationStrings(gate.EvidencePaths)
		environment, environmentErr := projectProcessEnvironment(gate.Environment)
		if !verificationIDPattern.MatchString(gate.ID) || !verificationIDPattern.MatchString(gate.Kind) ||
			seen[gate.ID] || gate.TimeoutSeconds < 1 || gate.TimeoutSeconds > 1800 ||
			len(gate.EvidenceKinds) == 0 || (gate.Available && (len(gate.Argv) == 0 || len(gate.Argv) > 128)) ||
			(!gate.Available && strings.TrimSpace(gate.UnavailableReason) == "") || environmentErr != nil {
			return fmt.Errorf("project: verification gate %q is invalid", gate.ID)
		}
		gate.Environment = environment
		for _, criterion := range gate.Criteria {
			if !criterionIDs[criterion] {
				return fmt.Errorf("project: verification gate covers an unknown criterion")
			}
		}
		seen[gate.ID] = true
	}
	return nil
}

func selectVerificationGates(gates []VerificationGate, ids []string,
	full bool) ([]VerificationGate, string, error) {
	if full {
		return append([]VerificationGate(nil), gates...), "full", nil
	}
	ids = trimVerificationStrings(ids)
	if len(ids) == 0 {
		return nil, "", fmt.Errorf("project: targeted verification requires gate_ids")
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	selected := []VerificationGate{}
	for _, gate := range gates {
		if wanted[gate.ID] {
			selected = append(selected, gate)
			delete(wanted, gate.ID)
		}
	}
	if len(wanted) > 0 {
		return nil, "", fmt.Errorf("project: targeted verification gate was not found")
	}
	return selected, "targeted", nil
}

func verificationCoverage(manifest VerificationManifest,
	results []VerificationGateResult) ([]string, []string) {
	resultsByGate := map[string]VerificationGateResult{}
	for _, result := range results {
		resultsByGate[result.GateID] = result
	}
	passed, uncovered := []string{}, []string{}
	for _, criterion := range manifest.Criteria {
		required := 0
		covered := true
		for _, gate := range manifest.Gates {
			if !gate.Required || !containsVerificationString(gate.Criteria, criterion.ID) {
				continue
			}
			required++
			result, ok := resultsByGate[gate.ID]
			if !ok || result.Status != "passed" ||
				!containsVerificationString(result.CriteriaCovered, criterion.ID) {
				covered = false
			}
		}
		if required > 0 && covered {
			passed = append(passed, criterion.ID)
		} else {
			uncovered = append(uncovered, criterion.ID)
		}
	}
	return passed, uncovered
}

func verificationRunStatus(run VerificationRun, manifest VerificationManifest, full bool) string {
	waived := false
	for _, result := range run.Results {
		switch result.Status {
		case "failed", "timeout", "environment", "cancelled", "unavailable":
			return "failed"
		case "waived":
			waived = true
		}
	}
	if waived {
		return "waived"
	}
	if !full {
		return "targeted_passed"
	}
	if len(run.Results) != len(manifest.Gates) || len(run.UncoveredCriteria) > 0 {
		return "incomplete"
	}
	return "passed"
}

func evaluateVerificationRepair(run VerificationRun, history []VerificationRun,
	maxAttempts int) VerificationRepairDecision {
	signatures := []string{}
	selectedGateIDs := map[string]bool{}
	for _, result := range run.Results {
		if result.Status == "waived" {
			continue
		}
		selectedGateIDs[result.GateID] = true
		if result.FailureSignature != "" {
			signatures = append(signatures, result.FailureSignature)
		}
	}
	sort.Strings(signatures)
	attempts := 1
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].ManifestID == run.ManifestID &&
			verificationRunIncludesGate(history[index], selectedGateIDs) {
			attempts++
		}
	}
	decision := VerificationRepairDecision{State: "complete", Reason: "All selected gates passed with current evidence.",
		Attempts: attempts, MaxAttempts: maxAttempts, FailureSignatures: signatures}
	if len(signatures) == 0 {
		for _, result := range run.Results {
			if result.Status != "passed" {
				continue
			}
			previous, ok := latestVerificationGateResult(history, run.ManifestID, result.GateID)
			if ok && previous.Status != "passed" {
				decision.State, decision.Reason = "stop_flaky",
					"Gate "+result.GateID+" passed once after a failure; one more consecutive pass is required to establish a stable repair."
				return decision
			}
		}
		return decision
	}
	for _, result := range run.Results {
		if result.Status == "timeout" {
			decision.State, decision.Reason = "stop_timeout", "A gate reached its explicit timeout; increase authority or fix the command before retrying."
			return decision
		}
		if result.Status == "environment" || result.Status == "unavailable" {
			decision.State, decision.Reason = "stop_environment", "Required verification is unavailable in the current environment."
			return decision
		}
	}
	if attempts >= maxAttempts {
		decision.State, decision.Reason = "stop_budget", "The bounded repair-attempt budget is exhausted."
		return decision
	}
	previousSignatures := []string{}
	for gateID := range selectedGateIDs {
		previous, ok := latestVerificationGateResult(history, run.ManifestID, gateID)
		if ok && previous.FailureSignature != "" {
			previousSignatures = append(previousSignatures, previous.FailureSignature)
		}
	}
	if len(previousSignatures) > 0 {
		sort.Strings(previousSignatures)
		if strings.Join(previousSignatures, ",") == strings.Join(signatures, ",") {
			decision.State, decision.Reason = "stop_stagnation", "The same failure signatures repeated without new evidence."
			return decision
		}
		if len(signatures) > len(previousSignatures) {
			decision.State, decision.Reason = "stop_regression", "The repair introduced additional failure signatures."
			return decision
		}
	}
	decision.State, decision.Reason = "diagnose_patch_rerun", "Diagnose these signatures, apply a revision-bound patch, and rerun the narrowest affected gates."
	return decision
}

func verificationRunIncludesGate(run VerificationRun, gateIDs map[string]bool) bool {
	for _, result := range run.Results {
		if result.Status != "waived" && gateIDs[result.GateID] {
			return true
		}
	}
	return false
}

func latestVerificationGateResult(history []VerificationRun, manifestID uuid.UUID,
	gateID string) (VerificationGateResult, bool) {
	for runIndex := len(history) - 1; runIndex >= 0; runIndex-- {
		if history[runIndex].ManifestID != manifestID {
			continue
		}
		for resultIndex := len(history[runIndex].Results) - 1; resultIndex >= 0; resultIndex-- {
			result := history[runIndex].Results[resultIndex]
			if result.GateID == gateID && result.Status != "waived" {
				return result, true
			}
		}
	}
	return VerificationGateResult{}, false
}

func verificationFailureSignature(gate VerificationGate, status string, exit *int, output string) string {
	exitValue := "none"
	if exit != nil {
		exitValue = strconv.Itoa(*exit)
	}
	normalized := strings.ToLower(strings.TrimSpace(output))
	if len(normalized) > 8192 {
		normalized = normalized[len(normalized)-8192:]
	}
	normalized = verificationVolatilePattern.ReplaceAllString(normalized, "<volatile>")
	digest := sha256.Sum256([]byte(gate.Kind + "\x00" + status + "\x00" + exitValue + "\x00" + normalized))
	return hex.EncodeToString(digest[:12])
}

func activeVerificationWaiver(waivers []VerificationWaiver, manifestID uuid.UUID,
	gateID string, now time.Time) *VerificationWaiver {
	for index := len(waivers) - 1; index >= 0; index-- {
		waiver := waivers[index]
		if waiver.ManifestID == manifestID && waiver.RevokedAt == nil && waiver.ExpiresAt.After(now) &&
			containsVerificationString(waiver.GateIDs, gateID) {
			return &waiver
		}
	}
	return nil
}

func verificationEvidenceKind(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg"):
		return "screenshot"
	case strings.HasSuffix(lower, ".zip") || strings.Contains(lower, "trace"):
		return "trace"
	case strings.Contains(lower, "coverage"):
		return "coverage"
	case strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".xml"):
		return "report"
	default:
		return "artifact"
	}
}

func verificationEnvironmentOverrides(environment []string) []string {
	allowed := map[string]bool{
		"LANG": true, "LC_ALL": true, "TERM": true, "CI": true,
		"NODE_ENV": true, "PORT": true,
	}
	result := []string{}
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found && allowed[name] {
			result = append(result, entry)
		}
	}
	return result
}

func verificationCriterionIDs(criteria []VerificationCriterion) []string {
	result := make([]string, 0, len(criteria))
	for _, criterion := range criteria {
		result = append(result, criterion.ID)
	}
	return result
}

func verificationGateSubset(gates []VerificationGate, ids []string) bool {
	known := map[string]bool{}
	for _, gate := range gates {
		known[gate.ID] = true
	}
	for _, id := range trimVerificationStrings(ids) {
		if !known[id] {
			return false
		}
	}
	return true
}

func verificationCriteriaSubset(criteria []VerificationCriterion, ids []string) bool {
	known := map[string]bool{}
	for _, criterion := range criteria {
		known[criterion.ID] = true
	}
	for _, id := range trimVerificationStrings(ids) {
		if !known[id] {
			return false
		}
	}
	return true
}

func trimVerificationStrings(values []string) []string {
	result, seen := []string{}, map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func containsVerificationString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
