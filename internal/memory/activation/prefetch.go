package activation

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/belief/premise"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/memory/hnsw"
	"github.com/paxlabs-inc/ion-agent/internal/session"
)

const prefetchLimit = 10

type SemanticSource interface {
	SearchSemantic(context.Context, string, int) ([]string, error)
}

type FTS5Source interface {
	SearchFTS5(context.Context, Request, int) ([]string, error)
}

type SalienceSource interface {
	ScanSalience(context.Context, string, int) ([]string, error)
}

type PremiseSource interface {
	RelevantPremises(context.Context, string) ([]string, error)
}

// Pipeline concurrently executes the four required prefetch branches.
type Pipeline struct {
	semantic SemanticSource
	fts      FTS5Source
	salience SalienceSource
	premises PremiseSource
}

func NewPipeline(
	semantic SemanticSource,
	fts FTS5Source,
	salience SalienceSource,
	premises PremiseSource,
) (*Pipeline, error) {
	if semantic == nil || fts == nil || salience == nil || premises == nil {
		return nil, fmt.Errorf("activation: all four prefetch sources are required")
	}
	return &Pipeline{
		semantic: semantic, fts: fts, salience: salience, premises: premises,
	}, nil
}

func (pipeline *Pipeline) Prefetch(
	ctx context.Context,
	request Request,
) (*PrefetchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Query) == "" ||
		strings.TrimSpace(request.UserID) == "" ||
		strings.TrimSpace(request.SessionID) == "" {
		return nil, fmt.Errorf("activation: complete prefetch scope is required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type branchResult struct {
		kind   int
		values []string
		err    error
	}
	results := make(chan branchResult, 4)
	go func() {
		values, err := pipeline.semantic.SearchSemantic(runCtx, request.Query, prefetchLimit)
		results <- branchResult{kind: 0, values: values, err: err}
	}()
	go func() {
		values, err := pipeline.fts.SearchFTS5(runCtx, request, prefetchLimit)
		results <- branchResult{kind: 1, values: values, err: err}
	}()
	go func() {
		values, err := pipeline.salience.ScanSalience(runCtx, request.UserID, prefetchLimit)
		results <- branchResult{kind: 2, values: values, err: err}
	}()
	go func() {
		values, err := pipeline.premises.RelevantPremises(runCtx, request.Query)
		results <- branchResult{kind: 3, values: values, err: err}
	}()

	prefetched := &PrefetchResult{}
	var firstErr error
	for range 4 {
		result := <-results
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
				cancel()
			}
			continue
		}
		switch result.kind {
		case 0:
			prefetched.HNSWResults = cloneStrings(result.values)
		case 1:
			prefetched.FTS5Results = cloneStrings(result.values)
		case 2:
			prefetched.SalienceHits = cloneStrings(result.values)
		case 3:
			prefetched.PremiseRefs = cloneStrings(result.values)
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return prefetched, nil
}

type VectorSearch interface {
	Search(context.Context, []float32, int) ([]hnsw.Match, error)
}

type KeyResolver interface {
	ResolveVectorKeys(context.Context, []uint64) ([]string, error)
}

// SemanticAdapter turns a text query into HNSW k-nearest memory content.
type SemanticAdapter struct {
	embedder hnsw.Embedder
	index    VectorSearch
	resolver KeyResolver
}

func NewSemanticAdapter(
	embedder hnsw.Embedder,
	index VectorSearch,
	resolver KeyResolver,
) (*SemanticAdapter, error) {
	if embedder == nil || index == nil || resolver == nil {
		return nil, fmt.Errorf("activation: semantic adapter dependencies are required")
	}
	return &SemanticAdapter{embedder: embedder, index: index, resolver: resolver}, nil
}

func (adapter *SemanticAdapter) SearchSemantic(
	ctx context.Context,
	query string,
	limit int,
) ([]string, error) {
	vector, err := adapter.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	matches, err := adapter.index.Search(ctx, vector, limit)
	if err != nil {
		return nil, err
	}
	keys := make([]uint64, len(matches))
	for index, match := range matches {
		keys[index] = match.Key
	}
	return adapter.resolver.ResolveVectorKeys(ctx, keys)
}

type SessionAuthorizer interface {
	AuthorizedSessions(context.Context, string, uuid.UUID) ([]uuid.UUID, error)
}

type SessionSearchStore interface {
	SearchMetadataAcrossSessions(
		context.Context, []uuid.UUID, string, int,
	) ([]session.Message, error)
}

// SessionFTSAdapter performs cross-session FTS only over the gateway-authorized
// session allowlist for the current user.
type SessionFTSAdapter struct {
	store      SessionSearchStore
	authorizer SessionAuthorizer
}

func NewSessionFTSAdapter(
	store SessionSearchStore,
	authorizer SessionAuthorizer,
) (*SessionFTSAdapter, error) {
	if store == nil || authorizer == nil {
		return nil, fmt.Errorf("activation: FTS store and authorizer are required")
	}
	return &SessionFTSAdapter{store: store, authorizer: authorizer}, nil
}

func (adapter *SessionFTSAdapter) SearchFTS5(
	ctx context.Context,
	request Request,
	limit int,
) ([]string, error) {
	current, err := uuid.Parse(request.SessionID)
	if err != nil {
		return nil, fmt.Errorf("activation: invalid session ID: %w", err)
	}
	authorized, err := adapter.authorizer.AuthorizedSessions(
		ctx, request.UserID, current,
	)
	if err != nil {
		return nil, err
	}
	// Session plaintext remains encrypted at rest and is deliberately absent
	// from FTS. Use FTS5 to select authorized memory metadata, then rank the
	// decrypted results in process so no plaintext search index is created.
	window := limit * 8
	if window < limit {
		window = limit
	}
	if window > 256 {
		window = 256
	}
	messages, err := adapter.store.SearchMetadataAcrossSessions(
		ctx,
		authorized,
		`memory_type:transcript OR memory_type:summary OR memory_type:"tool-event"`,
		window,
	)
	if err != nil {
		return nil, err
	}
	queryTerms := strings.Fields(strings.ToLower(request.Query))
	type rankedMessage struct {
		message session.Message
		score   int
	}
	ranked := make([]rankedMessage, 0, len(messages))
	for _, message := range messages {
		content := strings.ToLower(string(message.Content))
		score := 0
		for _, term := range queryTerms {
			term = strings.Trim(term, ".,:;!?()[]{}\"'")
			if term != "" && strings.Contains(content, term) {
				score++
			}
		}
		if score != 0 {
			ranked = append(ranked, rankedMessage{message: message, score: score})
		}
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].score != ranked[right].score {
			return ranked[left].score > ranked[right].score
		}
		if !ranked[left].message.CreatedAt.Equal(ranked[right].message.CreatedAt) {
			return ranked[left].message.CreatedAt.After(ranked[right].message.CreatedAt)
		}
		return ranked[left].message.ID.String() < ranked[right].message.ID.String()
	})
	if limit < len(ranked) {
		ranked = ranked[:limit]
	}
	result := make([]string, len(ranked))
	for index, item := range ranked {
		result[index] = fmt.Sprintf(
			"session:%s memory:%s %s",
			item.message.SessionID,
			item.message.MemoryType,
			item.message.Content,
		)
	}
	return result, nil
}

// CortexSalience scans current typed memories by declared importance, source
// trust, and recency without bypassing Cortex resolution.
type CortexSalience struct {
	store *cortex.Cortex
	clock interface{ Now() time.Time }
}

func NewCortexSalience(
	store *cortex.Cortex,
	clock interface{ Now() time.Time },
) (*CortexSalience, error) {
	if store == nil || clock == nil {
		return nil, fmt.Errorf("activation: Cortex and clock are required")
	}
	return &CortexSalience{store: store, clock: clock}, nil
}

func (scanner *CortexSalience) ScanSalience(
	ctx context.Context,
	userID string,
	limit int,
) ([]string, error) {
	if strings.TrimSpace(userID) == "" || limit < 0 {
		return nil, fmt.Errorf("activation: user and non-negative limit are required")
	}
	type candidate struct {
		id      uuid.UUID
		content string
		score   float64
	}
	var candidates []candidate
	now := scanner.clock.Now()
	for _, memoryType := range memory.Types() {
		for _, id := range scanner.store.ListByType(memoryType) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			resolved, err := scanner.store.Resolve(id)
			if err != nil {
				return nil, err
			}
			if resolved.Head.Actor != userID &&
				resolved.Head.Actor != scanner.store.Actor() {
				continue
			}
			age := now.Sub(resolved.Head.LastUpdatedAt)
			recency := 0.0
			if age >= 0 {
				recency = 1 / (1 + age.Hours()/24)
			}
			candidates = append(candidates, candidate{
				id:      id,
				content: string(resolved.Version.Data),
				score: resolved.Head.DeclaredImportance*0.5 +
					resolved.Head.SourceTrust*0.3 + recency*0.2,
			})
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].score == candidates[right].score {
			return candidates[left].id.String() < candidates[right].id.String()
		}
		return candidates[left].score > candidates[right].score
	})
	if limit < len(candidates) {
		candidates = candidates[:limit]
	}
	result := make([]string, len(candidates))
	for index, candidate := range candidates {
		result[index] = candidate.content
	}
	return result, nil
}

type LedgerPremises struct {
	ledger interface{ Active() []*premise.Premise }
}

func NewLedgerPremises(ledger interface{ Active() []*premise.Premise }) (*LedgerPremises, error) {
	if ledger == nil {
		return nil, fmt.Errorf("activation: premise ledger is required")
	}
	return &LedgerPremises{ledger: ledger}, nil
}

func (source *LedgerPremises) RelevantPremises(
	ctx context.Context,
	_ string,
) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	active := source.ledger.Active()
	result := make([]string, 0, len(active))
	for _, item := range active {
		if item != nil && strings.TrimSpace(item.Statement) != "" {
			result = append(result, item.Statement)
		}
	}
	return result, nil
}

type TranscriptStore interface {
	ListMessages(context.Context, uuid.UUID) ([]session.Message, error)
}

type SessionTranscript struct {
	store TranscriptStore
}

func NewSessionTranscript(store TranscriptStore) (*SessionTranscript, error) {
	if store == nil {
		return nil, fmt.Errorf("activation: transcript store is required")
	}
	return &SessionTranscript{store: store}, nil
}

func (source *SessionTranscript) Entries(
	ctx context.Context,
	request Request,
) ([]Entry, error) {
	sessionID, err := uuid.Parse(request.SessionID)
	if err != nil {
		return nil, fmt.Errorf("activation: invalid transcript session ID: %w", err)
	}
	messages, err := source.store.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, len(messages))
	for index, message := range messages {
		entries[index] = Entry{
			Tier:     TierTranscript,
			Content:  fmt.Sprintf("%s: %s", message.Role, message.Content),
			Salience: 0.6,
		}
	}
	return entries, nil
}

func cloneStrings(values []string) []string {
	return append([]string(nil), values...)
}
