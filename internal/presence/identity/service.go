package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"lukechampine.com/blake3"
)

const (
	soulStateKind  = "soul"
	soulStateScope = "production"
)

// StateStore is the encrypted durable boundary consumed by identity history.
type StateStore interface {
	SaveLivingState(context.Context, string, string, json.RawMessage) error
	LoadLivingState(context.Context, string, string) (json.RawMessage, error)
}

// ServiceClock makes version provenance deterministic in restart tests.
type ServiceClock interface{ Now() time.Time }

// Version is one approved, rollback-capable identity revision.
type Version struct {
	Number     uint64    `json:"number"`
	Hash       string    `json:"hash"`
	Content    string    `json:"content"`
	ApprovedAt time.Time `json:"approved_at"`
	Provenance string    `json:"provenance"`
}

// Proposal is an explicit candidate. It never changes the current file until
// a later authenticated confirmation applies it.
type Proposal struct {
	ID            uuid.UUID `json:"id"`
	ActorID       uuid.UUID `json:"actor_id"`
	BaseVersion   uint64    `json:"base_version"`
	CandidateHash string    `json:"candidate_hash"`
	Candidate     string    `json:"candidate"`
	Diff          string    `json:"diff"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	ResolvedAt    time.Time `json:"resolved_at,omitempty"`
}

type document struct {
	Version      int        `json:"version"`
	History      []Version  `json:"history"`
	Proposals    []Proposal `json:"proposals"`
	PendingWrite *Version   `json:"pending_write,omitempty"`
}

// Projection is safe authenticated operator state shared by browser and TUI.
type Projection struct {
	Current          Version    `json:"current"`
	History          []Version  `json:"history"`
	Proposals        []Proposal `json:"proposals"`
	PendingProposals []Proposal `json:"pending_proposals"`
	DirectWrite      bool       `json:"direct_write"`
}

// Service owns proposal, approval, atomic replacement, history, and rollback.
type Service struct {
	mu    sync.Mutex
	file  *File
	store StateStore
	clock ServiceClock
	state document
}

func NewService(
	ctx context.Context,
	file *File,
	store StateStore,
	clock ServiceClock,
) (*Service, error) {
	if file == nil || store == nil || clock == nil {
		return nil, fmt.Errorf("identity: service dependencies are required")
	}
	current, err := file.Load(ctx)
	if err != nil {
		return nil, err
	}
	service := &Service{file: file, store: store, clock: clock}
	raw, err := store.LoadLivingState(ctx, soulStateKind, soulStateScope)
	if errors.Is(err, sql.ErrNoRows) {
		service.state = document{
			Version: 1,
			History: []Version{{
				Number: 1, Hash: soulHash(current), Content: current,
				ApprovedAt: clock.Now().UTC(), Provenance: "secure bootstrap",
			}},
		}
		if err := service.save(ctx); err != nil {
			return nil, err
		}
		return service, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &service.state); err != nil {
		return nil, fmt.Errorf("identity: decode durable state: %w", err)
	}
	if err := service.validate(); err != nil {
		return nil, err
	}
	if service.state.PendingWrite != nil {
		if err := service.recoverPending(ctx, current); err != nil {
			return nil, err
		}
		current, err = file.Load(ctx)
		if err != nil {
			return nil, err
		}
	}
	latest := service.state.History[len(service.state.History)-1]
	if soulHash(current) != latest.Hash {
		return nil, fmt.Errorf("identity: SOUL.md changed outside approval history")
	}
	return service, nil
}

func (service *Service) Current(ctx context.Context) (Version, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.verifyCurrent(ctx); err != nil {
		return Version{}, err
	}
	return service.state.History[len(service.state.History)-1], nil
}

// Load implements the channel identity-provider boundary. It verifies the
// approved file against encrypted history on every interaction and returns only
// the current immutable identity anchor.
func (service *Service) Load(ctx context.Context) (string, error) {
	current, err := service.Current(ctx)
	if err != nil {
		return "", err
	}
	return current.Content, nil
}

func (service *Service) Projection(
	ctx context.Context,
	actorID uuid.UUID,
) (Projection, error) {
	if actorID == uuid.Nil {
		return Projection{}, fmt.Errorf("identity: authenticated actor is required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.verifyCurrent(ctx); err != nil {
		return Projection{}, err
	}
	history := append([]Version(nil), service.state.History...)
	var proposals []Proposal
	var pending []Proposal
	for _, proposal := range service.state.Proposals {
		if proposal.ActorID != actorID {
			continue
		}
		proposals = append(proposals, proposal)
		if proposal.Status == "pending" {
			pending = append(pending, proposal)
		}
	}
	return Projection{
		Current: history[len(history)-1], History: history, Proposals: proposals,
		PendingProposals: pending, DirectWrite: false,
	}, nil
}

func (service *Service) Propose(
	ctx context.Context,
	actorID uuid.UUID,
	candidate string,
) (Proposal, error) {
	if actorID == uuid.Nil {
		return Proposal{}, fmt.Errorf("identity: authenticated actor is required")
	}
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || len(candidate) > maxSoulBytes {
		return Proposal{}, fmt.Errorf("identity: bounded candidate is required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.verifyCurrent(ctx); err != nil {
		return Proposal{}, err
	}
	current := service.state.History[len(service.state.History)-1]
	if soulHash(candidate) == current.Hash {
		return Proposal{}, fmt.Errorf("identity: candidate does not change SOUL.md")
	}
	proposal := Proposal{
		ID: uuid.New(), ActorID: actorID, BaseVersion: current.Number,
		CandidateHash: soulHash(candidate), Candidate: candidate,
		Diff: soulDiff(current.Content, candidate), Status: "pending",
		CreatedAt: service.clock.Now().UTC(),
	}
	service.state.Proposals = append(service.state.Proposals, proposal)
	if err := service.save(ctx); err != nil {
		return Proposal{}, err
	}
	return proposal, nil
}

func (service *Service) Resolve(
	ctx context.Context,
	actorID uuid.UUID,
	proposalID uuid.UUID,
	approve bool,
) (Version, error) {
	if actorID == uuid.Nil || proposalID == uuid.Nil {
		return Version{}, fmt.Errorf("identity: actor and proposal are required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.verifyCurrent(ctx); err != nil {
		return Version{}, err
	}
	index := -1
	for candidateIndex := range service.state.Proposals {
		proposal := service.state.Proposals[candidateIndex]
		if proposal.ID == proposalID && proposal.ActorID == actorID {
			index = candidateIndex
			break
		}
	}
	if index < 0 {
		return Version{}, fmt.Errorf("identity: proposal not found in actor scope")
	}
	proposal := &service.state.Proposals[index]
	if proposal.Status != "pending" {
		return Version{}, fmt.Errorf("identity: proposal is already resolved")
	}
	proposal.ResolvedAt = service.clock.Now().UTC()
	if !approve {
		proposal.Status = "denied"
		if err := service.save(ctx); err != nil {
			return Version{}, err
		}
		return service.state.History[len(service.state.History)-1], nil
	}
	current := service.state.History[len(service.state.History)-1]
	if proposal.BaseVersion != current.Number {
		return Version{}, fmt.Errorf("identity: proposal base version is stale")
	}
	proposal.Status = "approved"
	next := Version{
		Number: current.Number + 1, Hash: proposal.CandidateHash,
		Content: proposal.Candidate, ApprovedAt: proposal.ResolvedAt,
		Provenance: "approved proposal " + proposal.ID.String() +
			" by actor " + actorID.String(),
	}
	if err := service.applyVersion(ctx, next); err != nil {
		return Version{}, err
	}
	return next, nil
}

func (service *Service) Rollback(
	ctx context.Context,
	actorID uuid.UUID,
	version uint64,
) (Version, error) {
	if actorID == uuid.Nil || version == 0 {
		return Version{}, fmt.Errorf("identity: actor and rollback version are required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.verifyCurrent(ctx); err != nil {
		return Version{}, err
	}
	var target *Version
	for index := range service.state.History {
		if service.state.History[index].Number == version {
			copy := service.state.History[index]
			target = &copy
			break
		}
	}
	if target == nil {
		return Version{}, fmt.Errorf("identity: rollback version not found")
	}
	current := service.state.History[len(service.state.History)-1]
	next := Version{
		Number: current.Number + 1, Hash: target.Hash,
		Content: target.Content, ApprovedAt: service.clock.Now().UTC(),
		Provenance: fmt.Sprintf(
			"approved rollback to version %d by actor %s", version, actorID,
		),
	}
	if err := service.applyVersion(ctx, next); err != nil {
		return Version{}, err
	}
	return next, nil
}

func (service *Service) applyVersion(ctx context.Context, next Version) error {
	service.state.PendingWrite = &next
	if err := service.save(ctx); err != nil {
		return err
	}
	if err := service.file.Replace(ctx, next.Content); err != nil {
		return err
	}
	service.state.History = append(service.state.History, next)
	service.state.PendingWrite = nil
	return service.save(ctx)
}

func (service *Service) recoverPending(ctx context.Context, current string) error {
	pending := *service.state.PendingWrite
	currentHash := soulHash(current)
	latest := service.state.History[len(service.state.History)-1]
	switch currentHash {
	case pending.Hash:
	case latest.Hash:
		if err := service.file.Replace(ctx, pending.Content); err != nil {
			return err
		}
	default:
		return fmt.Errorf("identity: pending write conflicts with SOUL.md")
	}
	service.state.History = append(service.state.History, pending)
	service.state.PendingWrite = nil
	return service.save(ctx)
}

func (service *Service) verifyCurrent(ctx context.Context) error {
	current, err := service.file.Load(ctx)
	if err != nil {
		return err
	}
	latest := service.state.History[len(service.state.History)-1]
	if soulHash(current) != latest.Hash {
		return fmt.Errorf("identity: SOUL.md changed outside approval history")
	}
	return nil
}

func (service *Service) validate() error {
	if service.state.Version != 1 || len(service.state.History) == 0 {
		return fmt.Errorf("identity: invalid durable state")
	}
	var previous uint64
	for _, version := range service.state.History {
		if version.Number != previous+1 || version.ApprovedAt.IsZero() ||
			strings.TrimSpace(version.Content) == "" ||
			len(version.Content) > maxSoulBytes ||
			version.Hash != soulHash(version.Content) {
			return fmt.Errorf("identity: invalid durable history")
		}
		previous = version.Number
	}
	for _, proposal := range service.state.Proposals {
		if proposal.ID == uuid.Nil || proposal.ActorID == uuid.Nil ||
			proposal.BaseVersion == 0 || proposal.CreatedAt.IsZero() ||
			proposal.CandidateHash != soulHash(proposal.Candidate) ||
			(proposal.Status != "pending" && proposal.Status != "approved" &&
				proposal.Status != "denied") {
			return fmt.Errorf("identity: invalid durable proposal")
		}
	}
	if service.state.PendingWrite != nil &&
		service.state.PendingWrite.Number != previous+1 {
		return fmt.Errorf("identity: invalid pending write")
	}
	return nil
}

func (service *Service) save(ctx context.Context) error {
	raw, err := json.Marshal(service.state)
	if err != nil {
		return fmt.Errorf("identity: encode durable state: %w", err)
	}
	return service.store.SaveLivingState(
		ctx, soulStateKind, soulStateScope, raw,
	)
}

func soulHash(content string) string {
	sum := blake3.Sum256([]byte(strings.TrimSpace(content)))
	return fmt.Sprintf("%x", sum[:])
}

func soulDiff(before string, after string) string {
	return "--- SOUL.md current\n+++ SOUL.md candidate\n-" +
		strings.ReplaceAll(strings.TrimSpace(before), "\n", "\n-") +
		"\n+" + strings.ReplaceAll(strings.TrimSpace(after), "\n", "\n+")
}
