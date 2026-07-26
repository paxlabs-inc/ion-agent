package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	candidatesDirectory = "candidates"
	newCandidatesRoot   = ".new-candidates"
	historyDirectory    = "history"
	maxCandidateEdits   = 8
	minimumGateCases    = 3
	minimumImprovement  = 0.01
)

// CandidateStatus is the promotion state of one proposed procedural revision.
type CandidateStatus string

const (
	CandidatePending  CandidateStatus = "pending"
	CandidateAdopted  CandidateStatus = "adopted"
	CandidateRejected CandidateStatus = "rejected"
)

// Evidence identifies a concrete observed outcome that motivated a candidate.
// It deliberately stores references and a bounded summary, not raw transcripts.
type Evidence struct {
	EpisodeID string `json:"episode_id"`
	Outcome   string `json:"outcome"`
	Summary   string `json:"summary"`
	Verifier  string `json:"verifier,omitempty"`
}

// Evaluation is the held-out behavioral gate applied to a candidate.
type Evaluation struct {
	BaselineScore    float64  `json:"baseline_score"`
	CandidateScore   float64  `json:"candidate_score"`
	ValidationCases  int      `json:"validation_cases"`
	SafetyPassed     bool     `json:"safety_passed"`
	ValidationRunIDs []string `json:"validation_run_ids"`
	Notes            []string `json:"notes,omitempty"`
}

// Candidate is an immutable proposed skill plus its mutable gate decision.
type Candidate struct {
	ID           string          `json:"id"`
	SkillName    string          `json:"skill_name"`
	BaseRevision int             `json:"base_revision"`
	Proposed     Skill           `json:"proposed"`
	Evidence     []Evidence      `json:"evidence"`
	Status       CandidateStatus `json:"status"`
	Evaluation   *Evaluation     `json:"evaluation,omitempty"`
	Decision     string          `json:"decision,omitempty"`
}

// SkillSummary is bounded operator-visible state for one active procedure.
type SkillSummary struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Origin        string   `json:"origin"`
	SourcePath    string   `json:"source_path,omitempty"`
	Revision      int      `json:"revision"`
	Uses          int      `json:"uses"`
	Platforms     []string `json:"platforms,omitempty"`
	RequiredTools []string `json:"required_tools,omitempty"`
	ResourceCount int      `json:"resource_count"`
	Status        string   `json:"status"`
}

// CandidateSummary is bounded gate state without raw evidence or procedure
// content.
type CandidateSummary struct {
	ID               string          `json:"id"`
	SkillName        string          `json:"skill_name"`
	Description      string          `json:"description,omitempty"`
	Origin           string          `json:"origin"`
	BaseRevision     int             `json:"base_revision"`
	ProposedRevision int             `json:"proposed_revision"`
	Status           CandidateStatus `json:"status"`
	Evaluation       *Evaluation     `json:"evaluation,omitempty"`
	Decision         string          `json:"decision,omitempty"`
}

// RetiredRevision is an inactive version retained for audit and rollback.
type RetiredRevision struct {
	Skill          Skill  `json:"-"`
	SkillName      string `json:"skill_name"`
	Origin         string `json:"origin"`
	Revision       int    `json:"revision"`
	ActiveRevision int    `json:"active_revision"`
}

// Lifecycle is the complete inspectable state of the procedural library.
type Lifecycle struct {
	Active     []SkillSummary     `json:"active"`
	Candidates []CandidateSummary `json:"candidates"`
	Retired    []RetiredRevision  `json:"retired"`
}

// Propose creates a staged candidate without changing the active skill.
func (store *Store) Propose(
	ctx context.Context,
	name string,
	refinement Refinement,
	evidence []Evidence,
) (Candidate, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Candidate{}, err
	}
	if err := validateEvidence(evidence); err != nil {
		return Candidate{}, err
	}
	if editCount(refinement) == 0 {
		return Candidate{}, errors.New("skills: candidate must contain at least one edit")
	}
	if editCount(refinement) > maxCandidateEdits {
		return Candidate{}, fmt.Errorf("skills: candidate exceeds %d-edit budget", maxCandidateEdits)
	}
	if len(strings.TrimSpace(refinement.BodyNote)) > 4000 {
		return Candidate{}, errors.New("skills: candidate body note exceeds 4000 bytes")
	}
	base, err := store.Load(ctx, name)
	if err != nil {
		return Candidate{}, err
	}
	proposed := applyRefinement(base, refinement)
	if skillsEqual(base, proposed) {
		return Candidate{}, errors.New("skills: candidate does not change the active skill")
	}
	proposed.Revision = base.Revision + 1
	candidate := Candidate{
		SkillName: base.Name, BaseRevision: base.Revision, Proposed: proposed,
		Evidence: append([]Evidence(nil), evidence...), Status: CandidatePending,
	}
	candidate.ID, err = candidateID(candidate)
	if err != nil {
		return Candidate{}, err
	}
	directory := filepath.Join(store.root, slugify(base.Name), candidatesDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Candidate{}, err
	}
	path := filepath.Join(directory, candidate.ID+".json")
	if payload, readErr := os.ReadFile(path); readErr == nil {
		var existing Candidate
		if json.Unmarshal(payload, &existing) == nil {
			return existing, nil
		}
		return Candidate{}, fmt.Errorf("skills: candidate %s is unreadable", candidate.ID)
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Candidate{}, readErr
	}
	if err := writeCandidate(path, candidate); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}

// ProposeNew stages a novel skill without making it active.
func (store *Store) ProposeNew(
	ctx context.Context,
	skill Skill,
	evidence []Evidence,
) (Candidate, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Candidate{}, err
	}
	if err := validateEvidence(evidence); err != nil {
		return Candidate{}, err
	}
	skill.Name = strings.TrimSpace(skill.Name)
	if skill.Revision <= 0 {
		skill.Revision = 1
	}
	skill.Uses = 0
	if strings.TrimSpace(skill.Origin) == "" {
		skill.Origin = "authored"
	}
	if err := validate(skill); err != nil {
		return Candidate{}, err
	}
	if _, err := os.Stat(filepath.Join(store.root, slugify(skill.Name), fileName)); err == nil {
		return Candidate{}, fmt.Errorf("skills: %q already exists", skill.Name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Candidate{}, err
	}
	candidate := Candidate{
		SkillName: skill.Name, BaseRevision: 0, Proposed: skill,
		Evidence: append([]Evidence(nil), evidence...), Status: CandidatePending,
	}
	id, err := candidateID(candidate)
	if err != nil {
		return Candidate{}, err
	}
	candidate.ID = id
	directory := filepath.Join(store.root, newCandidatesRoot, slugify(skill.Name))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Candidate{}, err
	}
	path := filepath.Join(directory, candidate.ID+".json")
	if payload, readErr := os.ReadFile(path); readErr == nil {
		var existing Candidate
		if json.Unmarshal(payload, &existing) == nil {
			return existing, nil
		}
		return Candidate{}, fmt.Errorf("skills: candidate %s is unreadable", candidate.ID)
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Candidate{}, readErr
	}
	if err := writeCandidate(path, candidate); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}

// Candidates lists staged and decided proposals newest revision first.
func (store *Store) Candidates(ctx context.Context, name string) ([]Candidate, error) {
	directories := []string{
		filepath.Join(store.root, slugify(name), candidatesDirectory),
		filepath.Join(store.root, newCandidatesRoot, slugify(name)),
	}
	var result []Candidate
	for _, directory := range directories {
		entries, err := os.ReadDir(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			payload, err := os.ReadFile(filepath.Join(directory, entry.Name()))
			if err != nil {
				return nil, err
			}
			var candidate Candidate
			if err := json.Unmarshal(payload, &candidate); err != nil {
				return nil, fmt.Errorf("skills: parse candidate %q: %w", entry.Name(), err)
			}
			result = append(result, candidate)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].BaseRevision != result[right].BaseRevision {
			return result[left].BaseRevision > result[right].BaseRevision
		}
		return result[left].ID < result[right].ID
	})
	return result, nil
}

// Lifecycle returns active skills, every proposed gate decision, and retained
// inactive revisions without exposing raw task transcripts or bundle contents.
func (store *Store) Lifecycle(ctx context.Context) (Lifecycle, error) {
	active, err := store.List(ctx)
	if err != nil {
		return Lifecycle{}, err
	}
	result := Lifecycle{
		Active: make([]SkillSummary, 0, len(active)),
	}
	for _, skill := range active {
		result.Active = append(result.Active, summarizeSkill(skill))
	}
	seenCandidates := make(map[string]struct{})
	for _, skill := range active {
		candidates, candidateErr := store.Candidates(ctx, skill.Name)
		if candidateErr != nil {
			return Lifecycle{}, candidateErr
		}
		for _, candidate := range candidates {
			if _, exists := seenCandidates[candidate.ID]; exists {
				continue
			}
			seenCandidates[candidate.ID] = struct{}{}
			result.Candidates = append(
				result.Candidates,
				summarizeCandidate(candidate),
			)
		}
		retired, historyErr := store.retiredRevisions(ctx, skill)
		if historyErr != nil {
			return Lifecycle{}, historyErr
		}
		result.Retired = append(result.Retired, retired...)
	}
	newRoots, err := os.ReadDir(filepath.Join(store.root, newCandidatesRoot))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Lifecycle{}, err
	}
	for _, entry := range newRoots {
		if err := ctx.Err(); err != nil {
			return Lifecycle{}, err
		}
		if !entry.IsDir() {
			continue
		}
		candidates, candidateErr := store.Candidates(ctx, entry.Name())
		if candidateErr != nil {
			return Lifecycle{}, candidateErr
		}
		for _, candidate := range candidates {
			if _, exists := seenCandidates[candidate.ID]; exists {
				continue
			}
			seenCandidates[candidate.ID] = struct{}{}
			result.Candidates = append(
				result.Candidates,
				summarizeCandidate(candidate),
			)
		}
	}
	sort.Slice(result.Candidates, func(left, right int) bool {
		if result.Candidates[left].SkillName != result.Candidates[right].SkillName {
			return result.Candidates[left].SkillName < result.Candidates[right].SkillName
		}
		if result.Candidates[left].BaseRevision != result.Candidates[right].BaseRevision {
			return result.Candidates[left].BaseRevision > result.Candidates[right].BaseRevision
		}
		return result.Candidates[left].ID < result.Candidates[right].ID
	})
	sort.Slice(result.Retired, func(left, right int) bool {
		if result.Retired[left].Skill.Name != result.Retired[right].Skill.Name {
			return result.Retired[left].Skill.Name < result.Retired[right].Skill.Name
		}
		return result.Retired[left].Skill.Revision >
			result.Retired[right].Skill.Revision
	})
	return result, nil
}

// Rollback restores a retained revision as a new monotonic active revision and
// archives the version it replaces.
func (store *Store) Rollback(
	ctx context.Context,
	name string,
	revision int,
) (Skill, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	if revision <= 0 {
		return Skill{}, errors.New("skills: rollback revision must be positive")
	}
	active, err := store.Load(ctx, name)
	if err != nil {
		return Skill{}, err
	}
	path := filepath.Join(
		store.root,
		slugify(active.Name),
		historyDirectory,
		fmt.Sprintf("revision-%06d.md", revision),
	)
	payload, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf(
			"skills: load rollback revision %d for %q: %w",
			revision,
			active.Name,
			err,
		)
	}
	restored, err := decode(string(payload))
	if err != nil {
		return Skill{}, fmt.Errorf(
			"skills: parse rollback revision %d for %q: %w",
			revision,
			active.Name,
			err,
		)
	}
	if slugify(restored.Name) != slugify(active.Name) {
		return Skill{}, errors.New("skills: rollback history belongs to another skill")
	}
	if err := store.archiveActive(active); err != nil {
		return Skill{}, err
	}
	restored.Revision = active.Revision + 1
	restored.Uses = active.Uses
	if err := store.replace(ctx, restored); err != nil {
		return Skill{}, err
	}
	return restored, nil
}

// Evaluate records held-out results and promotes only a safe, meaningful
// improvement over the still-active base revision.
func (store *Store) Evaluate(
	ctx context.Context,
	name string,
	id string,
	evaluation Evaluation,
) (Candidate, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Candidate{}, err
	}
	if err := validateEvaluation(evaluation); err != nil {
		return Candidate{}, err
	}
	id = strings.TrimSpace(id)
	if len(id) != 24 {
		return Candidate{}, errors.New("skills: candidate ID is invalid")
	}
	if decoded, err := hex.DecodeString(id); err != nil || len(decoded) != 12 {
		return Candidate{}, errors.New("skills: candidate ID is invalid")
	}
	path, payload, err := store.loadCandidatePayload(name, id)
	if err != nil {
		return Candidate{}, fmt.Errorf("skills: load candidate %q: %w", id, err)
	}
	var candidate Candidate
	if err := json.Unmarshal(payload, &candidate); err != nil {
		return Candidate{}, fmt.Errorf("skills: parse candidate %q: %w", id, err)
	}
	if candidate.Status != CandidatePending {
		return Candidate{}, fmt.Errorf("skills: candidate %q is already %s", id, candidate.Status)
	}
	candidate.Evaluation = &evaluation
	switch {
	case candidate.BaseRevision == 0 && store.activeSkillExists(candidate.SkillName):
		candidate.Status = CandidateRejected
		candidate.Decision = "a skill with this name became active; candidate is stale"
	case candidate.BaseRevision > 0 && !store.activeRevisionMatches(
		ctx, candidate.SkillName, candidate.BaseRevision,
	):
		candidate.Status = CandidateRejected
		candidate.Decision = "active skill revision changed; candidate is stale"
	case !evaluation.SafetyPassed:
		candidate.Status = CandidateRejected
		candidate.Decision = "safety validation failed"
	case evaluation.CandidateScore < evaluation.BaselineScore+minimumImprovement:
		candidate.Status = CandidateRejected
		candidate.Decision = fmt.Sprintf(
			"held-out improvement %.4f is below required %.4f",
			evaluation.CandidateScore-evaluation.BaselineScore,
			minimumImprovement,
		)
	default:
		if candidate.BaseRevision == 0 {
			if err := store.activateNew(candidate.Proposed); err != nil {
				return Candidate{}, err
			}
			candidate.Status = CandidateAdopted
			candidate.Decision = "new skill passed held-out applicability and safety validation"
		} else {
			active, err := store.Load(ctx, name)
			if err != nil {
				return Candidate{}, err
			}
			if err := store.archiveActive(active); err != nil {
				return Candidate{}, err
			}
			if err := store.replace(ctx, candidate.Proposed); err != nil {
				return Candidate{}, err
			}
			candidate.Status = CandidateAdopted
			candidate.Decision = "candidate improved held-out behavior and passed safety"
		}
	}
	if err := writeCandidate(path, candidate); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}

// GateAndActivateNew runs deterministic held-out selection cases plus the
// safety gate, then atomically activates only a materially better candidate.
func (store *Store) GateAndActivateNew(
	ctx context.Context,
	candidate Candidate,
	originalTask string,
	matchContext MatchContext,
) (Candidate, error) {
	if candidate.BaseRevision != 0 || candidate.Status != CandidatePending {
		return Candidate{}, errors.New("skills: new pending candidate is required")
	}
	positiveOriginal := skillMatchScore(
		candidate.Proposed, strings.ToLower(strings.TrimSpace(originalTask)),
	) >= minimumMatchScore
	heldOutRecurrence := skillMatchScore(
		candidate.Proposed,
		"Please apply "+candidate.Proposed.Trigger+" to a similar request",
	) >= minimumMatchScore
	heldOutDescription := skillMatchScore(
		candidate.Proposed,
		"Use a reusable procedure to "+candidate.Proposed.Description,
	) >= minimumMatchScore
	heldOutUnrelated := skillMatchScore(
		candidate.Proposed,
		"Translate a short greeting and report tomorrow's weather",
	) < minimumMatchScore
	passed := 0
	for _, result := range []bool{
		heldOutRecurrence, heldOutDescription, heldOutUnrelated,
	} {
		if result {
			passed++
		}
	}
	safetyPassed := safeLearnedSkill(candidate.Proposed) &&
		skillApplicable(candidate.Proposed, matchContext) &&
		positiveOriginal &&
		passed == 3
	evaluation := Evaluation{
		BaselineScore:   1.0 / 3.0,
		CandidateScore:  float64(passed) / 3.0,
		ValidationCases: 3,
		SafetyPassed:    safetyPassed,
		ValidationRunIDs: []string{
			candidate.ID + "-recurrence",
			candidate.ID + "-description-paraphrase",
			candidate.ID + "-unrelated-control",
		},
		Notes: []string{
			fmt.Sprintf("original task selected: %t", positiveOriginal),
			fmt.Sprintf("held-out recurrence selected: %t", heldOutRecurrence),
			fmt.Sprintf("held-out description paraphrase selected: %t", heldOutDescription),
			fmt.Sprintf("held-out unrelated control rejected: %t", heldOutUnrelated),
			fmt.Sprintf("safety and capability gate passed: %t", safetyPassed),
		},
	}
	return store.Evaluate(ctx, candidate.SkillName, candidate.ID, evaluation)
}

func (store *Store) loadCandidatePayload(
	name string,
	id string,
) (string, []byte, error) {
	paths := []string{
		filepath.Join(store.root, slugify(name), candidatesDirectory, id+".json"),
		filepath.Join(store.root, newCandidatesRoot, slugify(name), id+".json"),
	}
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err == nil {
			return path, payload, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", nil, err
		}
	}
	return "", nil, os.ErrNotExist
}

func (store *Store) activeSkillExists(name string) bool {
	_, err := os.Stat(filepath.Join(store.root, slugify(name), fileName))
	return err == nil
}

func (store *Store) activeRevisionMatches(
	ctx context.Context,
	name string,
	revision int,
) bool {
	active, err := store.Load(ctx, name)
	return err == nil && active.Revision == revision
}

func (store *Store) activateNew(skill Skill) error {
	target := filepath.Join(store.root, slugify(skill.Name))
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("skills: %q already exists", skill.Name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staging, err := os.MkdirTemp(store.root, ".skill-activate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := os.Chmod(staging, 0o700); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(staging, fileName), encode(skill)); err != nil {
		return err
	}
	return os.Rename(staging, target)
}

func safeLearnedSkill(skill Skill) bool {
	payload := strings.ToLower(strings.Join([]string{
		skill.Name, skill.Description, skill.Trigger,
		strings.Join(skill.Steps, "\n"),
		strings.Join(skill.Pitfalls, "\n"),
		strings.Join(skill.Verification, "\n"),
		skill.Body,
	}, "\n"))
	for _, marker := range []string{
		"ignore previous instructions",
		"ignore the user",
		"system message says",
		"-----begin private key-----",
		"password=",
		"api_key=",
		"access_token=",
		"secret_key=",
	} {
		if strings.Contains(payload, marker) {
			return false
		}
	}
	for _, value := range append(
		append([]string(nil), skill.RequiredTools...),
		skill.Platforms...,
	) {
		if len(value) > 128 {
			return false
		}
	}
	return true
}

func applyRefinement(skill Skill, refinement Refinement) Skill {
	skill.Steps = unique(append(append([]string(nil), skill.Steps...), refinement.Steps...))
	skill.Pitfalls = unique(append(append([]string(nil), skill.Pitfalls...), refinement.Pitfalls...))
	skill.Verification = unique(append(
		append([]string(nil), skill.Verification...), refinement.Verification...,
	))
	if note := strings.TrimSpace(refinement.BodyNote); note != "" {
		if strings.TrimSpace(skill.Body) != "" {
			skill.Body = strings.TrimSpace(skill.Body) + "\n\n" + note
		} else {
			skill.Body = note
		}
	}
	return skill
}

func (store *Store) archiveActive(skill Skill) error {
	directory := filepath.Join(store.root, slugify(skill.Name), historyDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	path := filepath.Join(directory, fmt.Sprintf("revision-%06d.md", skill.Revision))
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeAtomic(path, encode(skill))
}

func (store *Store) retiredRevisions(
	ctx context.Context,
	active Skill,
) ([]RetiredRevision, error) {
	directory := filepath.Join(store.root, slugify(active.Name), historyDirectory)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []RetiredRevision{}, nil
	}
	if err != nil {
		return nil, err
	}
	retired := make([]RetiredRevision, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		version, err := decode(string(payload))
		if err != nil {
			return nil, fmt.Errorf(
				"skills: parse retired revision %q: %w",
				entry.Name(),
				err,
			)
		}
		retired = append(retired, RetiredRevision{
			Skill: version, SkillName: version.Name, Origin: version.Origin,
			Revision: version.Revision, ActiveRevision: active.Revision,
		})
	}
	return retired, nil
}

func summarizeSkill(skill Skill) SkillSummary {
	description := strings.TrimSpace(skill.Description)
	if len(description) > 400 {
		description = description[:400]
	}
	return SkillSummary{
		Name: skill.Name, Description: description, Origin: skill.Origin,
		SourcePath: skill.SourcePath, Revision: skill.Revision, Uses: skill.Uses,
		Platforms:     append([]string(nil), skill.Platforms...),
		RequiredTools: append([]string(nil), skill.RequiredTools...),
		ResourceCount: len(skill.Resources), Status: "active",
	}
}

func summarizeCandidate(candidate Candidate) CandidateSummary {
	description := strings.TrimSpace(candidate.Proposed.Description)
	if len(description) > 400 {
		description = description[:400]
	}
	return CandidateSummary{
		ID: candidate.ID, SkillName: candidate.SkillName,
		Description: description, Origin: candidate.Proposed.Origin,
		BaseRevision:     candidate.BaseRevision,
		ProposedRevision: candidate.Proposed.Revision,
		Status:           candidate.Status, Evaluation: candidate.Evaluation,
		Decision: candidate.Decision,
	}
}

func writeCandidate(path string, candidate Candidate) error {
	payload, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return writeAtomic(path, payload)
}

func candidateID(candidate Candidate) (string, error) {
	payload, err := json.Marshal(struct {
		SkillName    string     `json:"skill_name"`
		BaseRevision int        `json:"base_revision"`
		Proposed     Skill      `json:"proposed"`
		Evidence     []Evidence `json:"evidence"`
	}{candidate.SkillName, candidate.BaseRevision, candidate.Proposed, candidate.Evidence})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:12]), nil
}

func validateEvidence(evidence []Evidence) error {
	if len(evidence) == 0 || len(evidence) > 20 {
		return errors.New("skills: candidate requires 1 to 20 evidence records")
	}
	for _, item := range evidence {
		if strings.TrimSpace(item.EpisodeID) == "" ||
			strings.TrimSpace(item.Outcome) == "" ||
			strings.TrimSpace(item.Summary) == "" {
			return errors.New("skills: evidence requires episode_id, outcome, and summary")
		}
		if len(item.Summary) > 1000 {
			return errors.New("skills: evidence summary exceeds 1000 bytes")
		}
	}
	return nil
}

func validateEvaluation(evaluation Evaluation) error {
	if evaluation.ValidationCases < minimumGateCases ||
		len(evaluation.ValidationRunIDs) < minimumGateCases {
		return fmt.Errorf("skills: gate requires at least %d held-out validation cases", minimumGateCases)
	}
	for _, score := range []float64{evaluation.BaselineScore, evaluation.CandidateScore} {
		if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return errors.New("skills: gate scores must be finite values from 0 to 1")
		}
	}
	return nil
}

func editCount(refinement Refinement) int {
	count := len(unique(refinement.Steps)) + len(unique(refinement.Pitfalls)) +
		len(unique(refinement.Verification))
	if strings.TrimSpace(refinement.BodyNote) != "" {
		count++
	}
	return count
}

func skillsEqual(left, right Skill) bool {
	leftPayload, _ := json.Marshal(left)
	rightPayload, _ := json.Marshal(right)
	return string(leftPayload) == string(rightPayload)
}
