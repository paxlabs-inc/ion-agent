// Package cortex implements typed, journal-authoritative durable memory.
package cortex

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"lukechampine.com/blake3"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
	"github.com/paxlabs-inc/ion-agent/internal/memory/mmr"
	"github.com/paxlabs-inc/ion-agent/internal/memory/smt"
)

var (
	// ErrNotFound indicates that a memory or version does not exist.
	ErrNotFound = errors.New("cortex: memory not found")
	// ErrTombstoned indicates that the latest memory view is archived.
	ErrTombstoned = errors.New("cortex: memory tombstoned")
	// ErrVersionConflict indicates a broken or non-contiguous version chain.
	ErrVersionConflict = errors.New("cortex: version conflict")
	// ErrInvalidSourceTrust indicates a trust score outside the closed [0,1] range.
	ErrInvalidSourceTrust = errors.New("cortex: source trust must be between 0 and 1")
)

// TrustLevel records confidence in the source of a memory. Arbitrary values in
// [0,1] are supported; the named levels are convenient policy defaults.
type TrustLevel float64

const (
	TrustUntrusted TrustLevel = 0
	TrustLow       TrustLevel = 0.25
	TrustMedium    TrustLevel = 0.50
	TrustHigh      TrustLevel = 0.75
	TrustVerified  TrustLevel = 1
)

// Valid reports whether the trust level is finite and within [0,1].
func (level TrustLevel) Valid() bool {
	value := float64(level)
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

// Memory represents a complete latest or historical memory view.
type Memory struct {
	Head    Head    `json:"head"`
	Version Version `json:"version"`
}

// Head is journal-derived metadata for a memory entry.
type Head struct {
	ID                 uuid.UUID   `json:"id"`
	Type               memory.Type `json:"type"`
	CurrentVersion     uint64      `json:"current_version"`
	Actor              string      `json:"actor"`
	Visibility         Visibility  `json:"visibility"`
	Tags               []string    `json:"tags,omitempty"`
	DeclaredImportance float64     `json:"declared_importance"`
	LastUpdatedAt      time.Time   `json:"last_updated_at"`
	Tombstoned         *Tombstone  `json:"tombstoned,omitempty"`
	SourceTrust        float64     `json:"source_trust"`
}

// Version is an immutable snapshot in a cryptographically linked chain.
type Version struct {
	ID              uuid.UUID   `json:"id"`
	Version         uint64      `json:"version"`
	PreviousVersion uint64      `json:"previous_version,omitempty"`
	PreviousHash    [32]byte    `json:"previous_hash,omitempty"`
	Type            memory.Type `json:"type"`
	Data            []byte      `json:"data"`
	Hash            [32]byte    `json:"hash"`
	CreatedAt       time.Time   `json:"created_at"`
	CreatedBy       string      `json:"created_by"`
	Confidence      float64     `json:"confidence"`
	SourceTrust     float64     `json:"source_trust"`
	ValidFrom       *time.Time  `json:"valid_from,omitempty"`
	ValidUntil      *time.Time  `json:"valid_until,omitempty"`
}

// Tombstone records a soft-delete. Versions remain available for audit.
type Tombstone struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
	By     string    `json:"by"`
}

// Visibility controls access scope.
type Visibility int

const (
	VisPrivate Visibility = iota
	VisShared
	VisPublic
)

// Cortex is a per-actor, journal-authoritative memory store.
type Cortex struct {
	mu       sync.RWMutex
	actor    string
	journal  *journal.Journal
	writer   *journal.Writer
	mmrState *mmr.MMR
	forest   *smt.Forest
	smts     map[memory.Type]*smt.Tree
	heads    map[uuid.UUID]*Head
	versions map[uuid.UUID][]*Version
	receipts map[uuid.UUID]Commitment
	clock    Clock
	closed   bool
}

// Commitment is the integrity receipt produced by applying a journal record.
type Commitment struct {
	JournalSeq   uint64
	MMRLeafIndex uint64
	MMRLeafHash  mmr.Hash
	MMRRoot      mmr.Hash
	SMTRoot      smt.Hash
}

// Clock abstracts time for deterministic tests.
type Clock interface {
	Now() time.Time
}

// Config configures a Cortex instance.
type Config struct {
	Actor   string
	Journal *journal.Journal
	Clock   Clock
}

// New replays the complete journal before starting its single writer goroutine.
func New(cfg Config) (*Cortex, error) {
	if cfg.Actor == "" {
		return nil, fmt.Errorf("cortex: actor is required")
	}
	if cfg.Journal == nil {
		return nil, fmt.Errorf("cortex: journal is required")
	}
	if cfg.Clock == nil {
		return nil, fmt.Errorf("cortex: clock is required")
	}

	forest, trees, err := newForest()
	if err != nil {
		return nil, err
	}
	store := &Cortex{
		actor:    cfg.Actor,
		journal:  cfg.Journal,
		mmrState: mmr.New(),
		forest:   forest,
		smts:     trees,
		heads:    make(map[uuid.UUID]*Head),
		versions: make(map[uuid.UUID][]*Version),
		receipts: make(map[uuid.UUID]Commitment),
		clock:    cfg.Clock,
	}
	if err := cfg.Journal.Replay(context.Background(), store.applyRecord); err != nil {
		return nil, fmt.Errorf("cortex: replay journal: %w", err)
	}
	store.writer, err = journal.NewWriter(cfg.Journal)
	if err != nil {
		return nil, fmt.Errorf("cortex: start journal writer: %w", err)
	}
	return store, nil
}

// Close drains accepted journal writes and stops the writer goroutine. The
// configured Journal remains caller-owned.
func (c *Cortex) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	writer := c.writer
	c.mu.Unlock()
	if writer == nil {
		return nil
	}
	return writer.Close()
}

// Write stores a new verified-source memory.
func (c *Cortex) Write(
	ctx context.Context,
	memType memory.Type,
	data []byte,
	createdBy string,
) (*Memory, error) {
	return c.WriteForActor(ctx, c.actor, memType, data, createdBy)
}

func (c *Cortex) WriteForActor(
	ctx context.Context,
	actor string,
	memType memory.Type,
	data []byte,
	createdBy string,
) (*Memory, error) {
	return c.writeForActorWithTrust(
		ctx, actor, memType, data, createdBy, TrustVerified,
	)
}

// WriteEvent commits a generic encrypted Event memory. It is the narrow audit
// sink used by components such as Cassandra.
func (c *Cortex) WriteEvent(
	ctx context.Context,
	data []byte,
	createdBy string,
) error {
	_, err := c.Write(ctx, memory.Event, data, createdBy)
	return err
}

// WriteWithTrust stores a new memory and its source trust level.
func (c *Cortex) WriteWithTrust(
	ctx context.Context,
	memType memory.Type,
	data []byte,
	createdBy string,
	sourceTrust TrustLevel,
) (*Memory, error) {
	return c.writeForActorWithTrust(
		ctx, c.actor, memType, data, createdBy, sourceTrust,
	)
}

func (c *Cortex) writeForActorWithTrust(
	ctx context.Context,
	actor string,
	memType memory.Type,
	data []byte,
	createdBy string,
	sourceTrust TrustLevel,
) (*Memory, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, fmt.Errorf("cortex: actor is required")
	}
	stored, _, err := c.writeWithIDAndCommitment(
		ctx, uuid.New(), actor, memType, data, createdBy, sourceTrust,
	)
	return stored, err
}

func (c *Cortex) writeWithIDAndCommitment(
	ctx context.Context,
	id uuid.UUID,
	actor string,
	memType memory.Type,
	data []byte,
	createdBy string,
	sourceTrust TrustLevel,
) (*Memory, Commitment, error) {
	if err := memType.Validate(); err != nil {
		return nil, Commitment{}, fmt.Errorf("cortex: invalid type: %w", err)
	}
	if !sourceTrust.Valid() {
		return nil, Commitment{}, ErrInvalidSourceTrust
	}
	if id == uuid.Nil {
		return nil, Commitment{}, fmt.Errorf("cortex: memory ID is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, Commitment{}, journal.ErrClosed
	}
	trust := float64(sourceTrust)
	record, err := c.writer.Append(ctx, journal.Entry{
		Type:        journal.Store,
		MemoryID:    id,
		MemoryType:  memType,
		Content:     append([]byte(nil), data...),
		Timestamp:   c.clock.Now().UnixNano(),
		Actor:       actor,
		CreatedBy:   createdBy,
		SourceTrust: &trust,
	})
	if err != nil {
		return nil, Commitment{}, fmt.Errorf("cortex: journal append: %w", err)
	}
	commitment, err := c.applyRecordWithCommitment(record)
	if err != nil {
		return nil, Commitment{}, fmt.Errorf("cortex: apply committed store: %w", err)
	}
	stored, err := c.resolveLocked(id, 0, false)
	return stored, commitment, err
}

// Resolve fetches the latest live memory by ID.
func (c *Cortex) Resolve(id uuid.UUID) (*Memory, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.resolveLocked(id, 0, false)
}

func (c *Cortex) ResolveForActor(
	actor string,
	id uuid.UUID,
) (*Memory, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	found, err := c.resolveLocked(id, 0, false)
	if err != nil {
		return nil, err
	}
	if found.Head.Visibility == VisPrivate && found.Head.Actor != actor {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return found, nil
}

// ResolveVersion fetches one immutable version, including versions of an
// archived memory.
func (c *Cortex) ResolveVersion(id uuid.UUID, version uint64) (*Memory, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.resolveLocked(id, version, true)
}

// ResolveVersionForActor fetches one immutable version while preserving the
// private actor boundary, including for archived memories.
func (c *Cortex) ResolveVersionForActor(
	actor string,
	id uuid.UUID,
	version uint64,
) (*Memory, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	found, err := c.resolveLocked(id, version, true)
	if err != nil {
		return nil, err
	}
	if found.Head.Visibility == VisPrivate && found.Head.Actor != actor {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return found, nil
}

// Versions returns defensive copies of the complete chain in ascending order.
func (c *Cortex) Versions(id uuid.UUID) ([]Version, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	chain, ok := c.versions[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	result := make([]Version, len(chain))
	for index, version := range chain {
		result[index] = cloneVersion(*version)
	}
	return result, nil
}

// Update creates a new version while retaining the current source trust.
func (c *Cortex) Update(
	ctx context.Context,
	id uuid.UUID,
	data []byte,
	createdBy string,
) (*Memory, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	head, err := c.mutableHeadLocked(id)
	if err != nil {
		return nil, err
	}
	return c.updateLocked(ctx, head, data, createdBy, TrustLevel(head.SourceTrust))
}

func (c *Cortex) UpdateForActor(
	ctx context.Context,
	actor string,
	id uuid.UUID,
	data []byte,
	createdBy string,
) (*Memory, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	head, err := c.mutableHeadLocked(id)
	if err != nil {
		return nil, err
	}
	if head.Visibility == VisPrivate && head.Actor != actor {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return c.updateLocked(ctx, head, data, createdBy, TrustLevel(head.SourceTrust))
}

// UpdateWithTrust creates a new content version with an explicitly reassessed
// source trust level.
func (c *Cortex) UpdateWithTrust(
	ctx context.Context,
	id uuid.UUID,
	data []byte,
	createdBy string,
	sourceTrust TrustLevel,
) (*Memory, error) {
	if !sourceTrust.Valid() {
		return nil, ErrInvalidSourceTrust
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	head, err := c.mutableHeadLocked(id)
	if err != nil {
		return nil, err
	}
	return c.updateLocked(ctx, head, data, createdBy, sourceTrust)
}

// UpdateSourceTrust records a trust reassessment as a new immutable version,
// preserving the current content.
func (c *Cortex) UpdateSourceTrust(
	ctx context.Context,
	id uuid.UUID,
	sourceTrust TrustLevel,
	updatedBy string,
) (*Memory, error) {
	if !sourceTrust.Valid() {
		return nil, ErrInvalidSourceTrust
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	head, err := c.mutableHeadLocked(id)
	if err != nil {
		return nil, err
	}
	chain := c.versions[id]
	data := append([]byte(nil), chain[len(chain)-1].Data...)
	return c.updateLocked(ctx, head, data, updatedBy, sourceTrust)
}

// SourceTrust returns the latest tracked source trust level.
func (c *Cortex) SourceTrust(id uuid.UUID) (TrustLevel, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	head, ok := c.heads[id]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return TrustLevel(head.SourceTrust), nil
}

// Tombstone soft-deletes a memory after durably journaling the mutation.
func (c *Cortex) Tombstone(
	ctx context.Context,
	id uuid.UUID,
	reason string,
	by string,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return journal.ErrClosed
	}
	head, ok := c.heads[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if head.Tombstoned != nil {
		return nil
	}
	content, err := json.Marshal(tombstonePayload{Reason: reason, By: by})
	if err != nil {
		return fmt.Errorf("cortex: encode tombstone: %w", err)
	}
	previous := head.CurrentVersion
	record, err := c.writer.Append(ctx, journal.Entry{
		Type:        journal.Archive,
		MemoryID:    id,
		MemoryType:  head.Type,
		Content:     content,
		PrevVersion: &previous,
		Timestamp:   c.clock.Now().UnixNano(),
		Actor:       head.Actor,
		CreatedBy:   by,
	})
	if err != nil {
		return fmt.Errorf("cortex: journal append: %w", err)
	}
	if err := c.applyRecord(record); err != nil {
		return fmt.Errorf("cortex: apply committed tombstone: %w", err)
	}
	return nil
}

// MMRState returns the append-only integrity accumulator.
func (c *Cortex) MMRState() *mmr.MMR {
	return c.mmrState
}

// SMT returns the namespace tree for a valid memory type.
func (c *Cortex) SMT(memType memory.Type) *smt.Tree {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.smts[memType]
}

// Actor returns the actor this cortex belongs to.
func (c *Cortex) Actor() string {
	return c.actor
}

// ListByType returns live memory IDs in stable UUID order.
func (c *Cortex) ListByType(memType memory.Type) []uuid.UUID {
	if !memType.Valid() {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]uuid.UUID, 0)
	for id, head := range c.heads {
		if head.Type == memType && head.Tombstoned == nil {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(left, right int) bool {
		return ids[left].String() < ids[right].String()
	})
	return ids
}

func (c *Cortex) ListByTypeForActor(
	actor string,
	memType memory.Type,
) []uuid.UUID {
	if !memType.Valid() || strings.TrimSpace(actor) == "" {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]uuid.UUID, 0)
	for id, head := range c.heads {
		if head.Type == memType && head.Tombstoned == nil &&
			(head.Visibility != VisPrivate || head.Actor == actor) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(left, right int) bool {
		return ids[left].String() < ids[right].String()
	})
	return ids
}

func (c *Cortex) updateLocked(
	ctx context.Context,
	head *Head,
	data []byte,
	createdBy string,
	sourceTrust TrustLevel,
) (*Memory, error) {
	if c.closed {
		return nil, journal.ErrClosed
	}
	if !sourceTrust.Valid() {
		return nil, ErrInvalidSourceTrust
	}
	previous := head.CurrentVersion
	trust := float64(sourceTrust)
	record, err := c.writer.Append(ctx, journal.Entry{
		Type:        journal.Update,
		MemoryID:    head.ID,
		MemoryType:  head.Type,
		Content:     append([]byte(nil), data...),
		PrevVersion: &previous,
		Timestamp:   c.clock.Now().UnixNano(),
		Actor:       head.Actor,
		CreatedBy:   createdBy,
		SourceTrust: &trust,
	})
	if err != nil {
		return nil, fmt.Errorf("cortex: journal append: %w", err)
	}
	if err := c.applyRecord(record); err != nil {
		return nil, fmt.Errorf("cortex: apply committed update: %w", err)
	}
	return c.resolveLocked(head.ID, 0, false)
}

func (c *Cortex) mutableHeadLocked(id uuid.UUID) (*Head, error) {
	if c.closed {
		return nil, journal.ErrClosed
	}
	head, ok := c.heads[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if head.Tombstoned != nil {
		return nil, fmt.Errorf("%w: %s", ErrTombstoned, id)
	}
	if len(c.versions[id]) == 0 {
		return nil, fmt.Errorf("%w: missing chain for %s", ErrVersionConflict, id)
	}
	return head, nil
}

func (c *Cortex) resolveLocked(
	id uuid.UUID,
	versionNumber uint64,
	includeTombstoned bool,
) (*Memory, error) {
	head, ok := c.heads[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if head.Tombstoned != nil && !includeTombstoned {
		return nil, fmt.Errorf("%w: %s", ErrTombstoned, id)
	}
	chain := c.versions[id]
	if len(chain) == 0 {
		return nil, fmt.Errorf("%w: missing chain for %s", ErrVersionConflict, id)
	}
	index := len(chain) - 1
	if versionNumber != 0 {
		if versionNumber > uint64(len(chain)) ||
			chain[versionNumber-1].Version != versionNumber {
			return nil, fmt.Errorf("%w: %s version %d", ErrNotFound, id, versionNumber)
		}
		index = int(versionNumber - 1)
	}
	return &Memory{
		Head:    cloneHead(*head),
		Version: cloneVersion(*chain[index]),
	}, nil
}

func (c *Cortex) applyRecord(record journal.Record) error {
	_, err := c.applyRecordWithCommitment(record)
	return err
}

func (c *Cortex) applyRecordWithCommitment(record journal.Record) (Commitment, error) {
	entry := record.Entry
	switch entry.Type {
	case journal.Store:
		if _, exists := c.heads[entry.MemoryID]; exists {
			return Commitment{}, fmt.Errorf("%w: duplicate store for %s", ErrVersionConflict, entry.MemoryID)
		}
		trust := entryTrust(entry, TrustVerified)
		version := buildVersion(entry, 1, Version{}, trust)
		c.heads[entry.MemoryID] = &Head{
			ID:             entry.MemoryID,
			Type:           entry.MemoryType,
			CurrentVersion: 1,
			Actor:          firstNonEmpty(entry.Actor, c.actor),
			Visibility:     VisPrivate,
			LastUpdatedAt:  version.CreatedAt,
			SourceTrust:    trust,
		}
		c.versions[entry.MemoryID] = []*Version{version}

	case journal.Update:
		head, exists := c.heads[entry.MemoryID]
		if !exists || head.Tombstoned != nil || entry.MemoryType != head.Type {
			return Commitment{}, fmt.Errorf("%w: update target %s", ErrVersionConflict, entry.MemoryID)
		}
		if entry.PrevVersion == nil || *entry.PrevVersion != head.CurrentVersion {
			return Commitment{}, fmt.Errorf("%w: update %s expected previous %d", ErrVersionConflict, entry.MemoryID, head.CurrentVersion)
		}
		chain := c.versions[entry.MemoryID]
		if len(chain) != int(head.CurrentVersion) {
			return Commitment{}, fmt.Errorf("%w: non-contiguous chain for %s", ErrVersionConflict, entry.MemoryID)
		}
		previous := *chain[len(chain)-1]
		trust := entryTrust(entry, TrustLevel(head.SourceTrust))
		version := buildVersion(entry, head.CurrentVersion+1, previous, trust)
		c.versions[entry.MemoryID] = append(chain, version)
		head.CurrentVersion = version.Version
		head.LastUpdatedAt = version.CreatedAt
		head.SourceTrust = trust

	case journal.Archive:
		head, exists := c.heads[entry.MemoryID]
		if !exists || head.Tombstoned != nil || entry.MemoryType != head.Type {
			return Commitment{}, fmt.Errorf("%w: archive target %s", ErrVersionConflict, entry.MemoryID)
		}
		if entry.PrevVersion == nil || *entry.PrevVersion != head.CurrentVersion {
			return Commitment{}, fmt.Errorf("%w: archive %s expected previous %d", ErrVersionConflict, entry.MemoryID, head.CurrentVersion)
		}
		var payload tombstonePayload
		if err := json.Unmarshal(entry.Content, &payload); err != nil {
			return Commitment{}, fmt.Errorf("cortex: decode tombstone: %w", err)
		}
		head.Tombstoned = &Tombstone{
			Reason: payload.Reason,
			At:     time.Unix(0, entry.Timestamp).UTC(),
			By:     payload.By,
		}
		head.LastUpdatedAt = head.Tombstoned.At

	default:
		return Commitment{}, fmt.Errorf("cortex: unsupported journal mutation %q", entry.Type)
	}

	tree := c.smts[entry.MemoryType]
	if tree == nil {
		return Commitment{}, fmt.Errorf("cortex: missing SMT namespace %s", entry.MemoryType)
	}
	smtRoot := tree.Update(smt.Key(entry.MemoryID[:]), record.Ciphertext)
	forestRoot := c.forest.Root()
	var commitment mmr.Hash
	copy(commitment[:], forestRoot[:])
	leafIndex, mmrRoot, err := c.mmrState.AppendEntry(record.Ciphertext, commitment)
	if err != nil {
		return Commitment{}, fmt.Errorf("cortex: append MMR: %w", err)
	}
	leafHash, ok := c.mmrState.Leaf(leafIndex)
	if !ok {
		return Commitment{}, fmt.Errorf("cortex: appended MMR leaf unavailable")
	}
	receipt := Commitment{
		JournalSeq:   entry.JournalSeq,
		MMRLeafIndex: leafIndex,
		MMRLeafHash:  leafHash,
		MMRRoot:      mmrRoot,
		SMTRoot:      smtRoot,
	}
	c.receipts[entry.MemoryID] = receipt
	return receipt, nil
}

type tombstonePayload struct {
	Reason string `json:"reason"`
	By     string `json:"by"`
}

func newForest() (*smt.Forest, map[memory.Type]*smt.Tree, error) {
	forest := smt.NewForest()
	trees := make(map[memory.Type]*smt.Tree, len(memory.Types()))
	for _, memType := range memory.Types() {
		if _, err := forest.Update(string(memType), smt.Hash{}, nil); err != nil {
			return nil, nil, fmt.Errorf("cortex: initialize SMT %s: %w", memType, err)
		}
		tree, ok := forest.Tree(string(memType))
		if !ok {
			return nil, nil, fmt.Errorf("cortex: initialize SMT %s", memType)
		}
		trees[memType] = tree
	}
	return forest, trees, nil
}

func entryTrust(entry journal.Entry, fallback TrustLevel) float64 {
	if entry.SourceTrust == nil {
		return float64(fallback)
	}
	return *entry.SourceTrust
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func buildVersion(
	entry journal.Entry,
	number uint64,
	previous Version,
	sourceTrust float64,
) *Version {
	version := &Version{
		ID:          entry.MemoryID,
		Version:     number,
		Type:        entry.MemoryType,
		Data:        append([]byte(nil), entry.Content...),
		CreatedAt:   time.Unix(0, entry.Timestamp).UTC(),
		CreatedBy:   entry.CreatedBy,
		Confidence:  1,
		SourceTrust: sourceTrust,
	}
	if number > 1 {
		version.PreviousVersion = previous.Version
		version.PreviousHash = previous.Hash
	}
	version.Hash = hashVersion(*version)
	return version
}

func hashVersion(version Version) [32]byte {
	hasher := blake3.New(32, nil)
	writeBytes(hasher, []byte("PROM-CORTEX-VERSION\x00"))
	writeBytes(hasher, version.ID[:])
	writeBytes(hasher, []byte(version.Type))
	var integer [8]byte
	binary.BigEndian.PutUint64(integer[:], version.Version)
	writeBytes(hasher, integer[:])
	binary.BigEndian.PutUint64(integer[:], version.PreviousVersion)
	writeBytes(hasher, integer[:])
	writeBytes(hasher, version.PreviousHash[:])
	binary.BigEndian.PutUint64(integer[:], uint64(version.CreatedAt.UnixNano()))
	writeBytes(hasher, integer[:])
	writeBytes(hasher, []byte(version.CreatedBy))
	binary.BigEndian.PutUint64(integer[:], math.Float64bits(version.SourceTrust))
	writeBytes(hasher, integer[:])
	writeBytes(hasher, version.Data)
	var result [32]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeBytes(writer byteWriter, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write(value)
}

func cloneHead(head Head) Head {
	head.Tags = append([]string(nil), head.Tags...)
	if head.Tombstoned != nil {
		tombstone := *head.Tombstoned
		head.Tombstoned = &tombstone
	}
	return head
}

func cloneVersion(version Version) Version {
	version.Data = append([]byte(nil), version.Data...)
	if version.ValidFrom != nil {
		validFrom := *version.ValidFrom
		version.ValidFrom = &validFrom
	}
	if version.ValidUntil != nil {
		validUntil := *version.ValidUntil
		version.ValidUntil = &validUntil
	}
	return version
}
