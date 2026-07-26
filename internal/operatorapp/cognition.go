package operatorapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/belief/premise"
	"github.com/paxlabs-inc/ion-agent/internal/belief/selfmodel"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/intent/prediction"
	"github.com/paxlabs-inc/ion-agent/internal/intent/taskgraph"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/activation"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/memory/hnsw"
	"github.com/paxlabs-inc/ion-agent/internal/reflection/cassandra"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const productionTaskConvergenceWindow = 3

type sessionCognition struct {
	premises    *premise.Ledger
	predictions *prediction.Engine
	taskGraph   *taskgraph.TaskGraph
	cassandra   *cassandra.Controller
	activation  *activation.Service
}

type cognitionSnapshot struct {
	Version     int                 `json:"version"`
	Premises    premise.Snapshot    `json:"premises"`
	Predictions prediction.State    `json:"predictions"`
	TaskGraph   taskgraph.State     `json:"task_graph"`
	Cassandra   cassandra.State     `json:"cassandra"`
	SelfModel   selfmodel.SelfModel `json:"self_model"`
}

type cognitionRegistry struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]*sessionCognition
}

func (capabilities *productionCapabilities) openCognition(
	ctx context.Context,
	clock types.Clock,
	config RuntimeConfig,
	sessions *session.Store,
) error {
	if sessions == nil {
		return fmt.Errorf("operator cognition: session store is required")
	}
	var err error
	core := selfmodel.NewImmutableCore([]selfmodel.SafetyConstraint{
		{
			ID:        "policy-bound-tools",
			Statement: "Every tool call remains subject to production policy.",
			Source:    "production composition root", Immutable: true,
			CreatedAt: clock.Now(),
		},
		{
			ID:        "no-emotional-safety-authority",
			Statement: "Behavioral state cannot weaken safety or approval.",
			Source:    "production composition root", Immutable: true,
			CreatedAt: clock.Now(),
		},
		{
			ID:        "honest-partials",
			Statement: "Incomplete work must not be represented as complete.",
			Source:    "production composition root", Immutable: true,
			CreatedAt: clock.Now(),
		},
	})
	codeRoot := strings.TrimSpace(config.SelfModelCodeRoot)
	if codeRoot == "" {
		codeRoot, err = findCodeRoot()
		if err != nil {
			capabilities.selfModel, err = selfmodel.NewFromBuildInfo(clock, core)
			if err != nil {
				return fmt.Errorf(
					"operator cognition: derive binary self-model: %w",
					err,
				)
			}
		}
	}
	if capabilities.selfModel == nil {
		capabilities.selfModel, err = selfmodel.NewFromCodeGraph(
			ctx, clock, core, codeRoot,
		)
		if err != nil {
			return fmt.Errorf("operator cognition: derive self-model: %w", err)
		}
	}

	embedder, err := hnsw.NewHashEmbedder(hnsw.DefaultEmbeddingDimensions)
	if err != nil {
		return err
	}
	vectorStore, err := hnsw.OpenSQLiteStore(
		ctx,
		filepath.Join(config.DataDirectory, "cortex", "vectors.db"),
		embedder.Dimensions(),
	)
	if err != nil {
		return err
	}
	socketPath := strings.TrimSpace(config.HNSWSocketPath)
	if socketPath == "" {
		socketPath = filepath.Join(config.DataDirectory, "cortex", "hnsw.sock")
	}
	remote, err := hnsw.NewClient(socketPath, embedder.Dimensions(), 750*time.Millisecond)
	if err != nil {
		_ = vectorStore.Close()
		return err
	}
	source, err := hnsw.NewCortexJournalSource(capabilities.memoryJournal, embedder)
	if err != nil {
		_ = remote.Close()
		_ = vectorStore.Close()
		return err
	}
	capabilities.memoryIndex, err = hnsw.NewIndex(ctx, hnsw.Config{
		Remote: remote, Store: vectorStore, Source: source,
		Dimensions: embedder.Dimensions(),
	})
	if err != nil {
		_ = remote.Close()
		_ = vectorStore.Close()
		return fmt.Errorf("operator cognition: open HNSW index: %w", err)
	}
	capabilities.cognition = &cognitionRegistry{
		sessions: make(map[uuid.UUID]*sessionCognition),
	}
	return nil
}

func findCodeRoot() (string, error) {
	current, err := filepath.Abs(".")
	if err != nil {
		return "", fmt.Errorf("operator cognition: resolve code root: %w", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(current, "go.mod")); statErr == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("operator cognition: locate code-derived self-model root")
		}
		current = parent
	}
}

func (capabilities *productionCapabilities) loopDeps(
	ctx context.Context,
	sessionID uuid.UUID,
	generator agent.Generator,
	model string,
	sessions *session.Store,
) (*agent.LoopDeps, error) {
	registry := capabilities.cognition
	if registry == nil {
		return nil, fmt.Errorf("operator cognition: registry is not initialized")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state := registry.sessions[sessionID]
	if state == nil {
		var err error
		state, err = capabilities.newSessionCognition(
			ctx, sessionID, generator, model, sessions,
		)
		if err != nil {
			return nil, err
		}
		registry.sessions[sessionID] = state
	}
	emotional, breaker, err := capabilities.living.Dependencies(
		ctx, sessionID,
	)
	if err != nil {
		return nil, err
	}
	return &agent.LoopDeps{
		Premises:          state.premises,
		PremiseExtractor:  productionPremiseExtractor(generator, model),
		Predictions:       state.predictions,
		PredictionRecords: capabilities.memory,
		TaskGraph:         state.taskGraph,
		SelfModel:         capabilities.selfModel,
		Cassandra:         state.cassandra,
		Events:            capabilities.memory,
		EventCommitter:    capabilities.memory,
		Citations:         capabilities.memory,
		CircuitBreaker:    breaker,
		MemoryActivation:  state.activation,
		Behavioral:        emotional,
		DecisionPolicy:    capabilities.living,
		ContextComposer: workAwareContextComposer{
			living: capabilities.living,
			work:   capabilities.work,
		},
		Checkpoints: productionCheckpointSink{
			capabilities: capabilities,
			sessionID:    sessionID,
			state:        state,
			sessions:     sessions,
		},
		Clock: capabilities.clock,
	}, nil
}

func (capabilities *productionCapabilities) newSessionCognition(
	ctx context.Context,
	sessionID uuid.UUID,
	generator agent.Generator,
	model string,
	sessions *session.Store,
) (*sessionCognition, error) {
	actorID, err := capabilities.living.Owner(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	rawSnapshot, loadErr := sessions.LoadCognitionState(ctx, sessionID)
	if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
		return nil, loadErr
	}
	var saved *cognitionSnapshot
	if loadErr == nil {
		var decoded cognitionSnapshot
		if err := json.Unmarshal(rawSnapshot, &decoded); err != nil {
			return nil, fmt.Errorf("operator cognition: decode durable state: %w", err)
		}
		if decoded.Version != 1 {
			return nil, fmt.Errorf("operator cognition: unsupported durable state version")
		}
		saved = &decoded
	}
	ledger, err := premise.New(capabilities.clock)
	if err != nil {
		return nil, err
	}
	detector := prediction.LayeredDetector{
		Fallback: prediction.ModelDetector{Provider: generator, Model: model},
	}
	engine, err := prediction.NewEngine(
		capabilities.clock, detector, prediction.DefaultMismatchThreshold,
	)
	if err != nil {
		return nil, err
	}
	auditor, err := cassandra.NewJournalAuditor(capabilities.memory, "cassandra")
	if err != nil {
		return nil, err
	}
	doubt, err := cassandra.New(capabilities.clock, auditor)
	if err != nil {
		return nil, err
	}
	graph := taskgraph.New("operator turn", productionTaskConvergenceWindow)
	if saved != nil {
		ledger, err = premise.Restore(capabilities.clock, saved.Premises)
		if err != nil {
			return nil, fmt.Errorf("operator cognition: restore premises: %w", err)
		}
		engine, err = prediction.RestoreEngine(
			capabilities.clock, detector, saved.Predictions,
		)
		if err != nil {
			return nil, fmt.Errorf("operator cognition: restore predictions: %w", err)
		}
		graph, err = taskgraph.Restore(saved.TaskGraph)
		if err != nil {
			return nil, fmt.Errorf("operator cognition: restore task graph: %w", err)
		}
		doubt, err = cassandra.Restore(
			capabilities.clock, auditor, saved.Cassandra,
		)
		if err != nil {
			return nil, fmt.Errorf("operator cognition: restore Cassandra: %w", err)
		}
		if err := capabilities.selfModel.ApplySnapshot(saved.SelfModel); err != nil {
			return nil, fmt.Errorf("operator cognition: restore self-model: %w", err)
		}
	}
	premiseSource, err := activation.NewLedgerPremises(ledger)
	if err != nil {
		return nil, err
	}
	semanticAdapter, err := activation.NewSemanticAdapter(
		mustEmbedder(), capabilities.memoryIndex,
		cortexKeyResolver{store: capabilities.memory, actor: actorID.String()},
	)
	if err != nil {
		return nil, err
	}
	semantic := reconcilingSemanticSource{
		index: capabilities.memoryIndex, inner: semanticAdapter,
	}
	fts, err := activation.NewSessionFTSAdapter(
		sessions, livingSessionAuthorizer{living: capabilities.living},
	)
	if err != nil {
		return nil, err
	}
	salience, err := activation.NewCortexSalience(
		capabilities.memory, capabilities.clock,
	)
	if err != nil {
		return nil, err
	}
	prefetch, err := activation.NewPipeline(
		semantic, fts, salience, premiseSource,
	)
	if err != nil {
		return nil, err
	}
	transcript, err := activation.NewSessionTranscript(sessions)
	if err != nil {
		return nil, err
	}
	service, err := activation.NewService(
		activation.DefaultTokenBudget,
		prefetch,
		productionTierSource{
			store: capabilities.memory, transcript: transcript,
			sessionID: sessionID, clock: capabilities.clock,
		},
	)
	if err != nil {
		return nil, err
	}
	return &sessionCognition{
		premises:    ledger,
		predictions: engine,
		taskGraph:   graph,
		cassandra:   doubt,
		activation:  service,
	}, nil
}

type productionCheckpointSink struct {
	capabilities *productionCapabilities
	sessionID    uuid.UUID
	state        *sessionCognition
	sessions     *session.Store
}

func (sink productionCheckpointSink) SaveCheckpoint(
	ctx context.Context,
	checkpoint agent.TurnCheckpoint,
) error {
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok || scope.TurnID == nil || *scope.TurnID == uuid.Nil {
		return fmt.Errorf("operator cognition: authenticated turn scope is required")
	}
	encodedCheckpoint, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("operator cognition: encode turn checkpoint: %w", err)
	}
	if err := sink.sessions.SaveTurnCheckpoint(
		ctx, *scope.TurnID, encodedCheckpoint,
	); err != nil {
		return err
	}
	snapshot := cognitionSnapshot{
		Version:     1,
		Premises:    sink.state.premises.Snapshot(),
		Predictions: sink.state.predictions.Snapshot(),
		TaskGraph:   sink.state.taskGraph.Snapshot(),
		Cassandra:   sink.state.cassandra.Snapshot(),
		SelfModel:   sink.capabilities.selfModel.Snapshot(),
	}
	encodedCognition, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("operator cognition: encode durable state: %w", err)
	}
	return sink.sessions.SaveCognitionState(ctx, sink.sessionID, encodedCognition)
}

func productionPremiseExtractor(
	generator agent.Generator,
	model string,
) premise.Extractor {
	layered := premise.LayeredExtractor{
		Deterministic: premise.DeterministicExtractor{},
	}
	if generator != nil && strings.TrimSpace(model) != "" && model != "unconfigured" {
		layered.Model = premise.ModelExtractor{Provider: generator, Model: model}
	}
	return layered
}

func mustEmbedder() hnsw.Embedder {
	embedder, err := hnsw.NewHashEmbedder(hnsw.DefaultEmbeddingDimensions)
	if err != nil {
		panic(err)
	}
	return embedder
}

type livingSessionAuthorizer struct {
	living *livingContext
}

func (authorizer livingSessionAuthorizer) AuthorizedSessions(
	ctx context.Context,
	userID string,
	current uuid.UUID,
) ([]uuid.UUID, error) {
	if authorizer.living == nil {
		return nil, fmt.Errorf("operator cognition: living session authorizer is unavailable")
	}
	return authorizer.living.AuthorizedSessions(ctx, userID, current)
}

type cortexKeyResolver struct {
	store *cortex.Cortex
	actor string
}

type reconcilingSemanticSource struct {
	index *hnsw.Index
	inner *activation.SemanticAdapter
}

func (source reconcilingSemanticSource) SearchSemantic(
	ctx context.Context,
	query string,
	limit int,
) ([]string, error) {
	if _, err := source.index.Reconcile(ctx); err != nil &&
		!errors.Is(err, hnsw.ErrServiceUnavailable) {
		return nil, err
	}
	return source.inner.SearchSemantic(ctx, query, limit)
}

func (resolver cortexKeyResolver) ResolveVectorKeys(
	ctx context.Context,
	keys []uint64,
) ([]string, error) {
	requested := make(map[uint64]struct{}, len(keys))
	for _, key := range keys {
		requested[key] = struct{}{}
	}
	values := make(map[uint64]string, len(keys))
	for _, memoryType := range memory.Types() {
		for _, id := range resolver.store.ListByType(memoryType) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			key := hnsw.KeyForMemoryID(id)
			if _, ok := requested[key]; !ok {
				continue
			}
			resolved, err := resolver.store.Resolve(id)
			if err != nil {
				return nil, err
			}
			if resolved.Head.Actor != resolver.actor &&
				resolved.Head.Actor != resolver.store.Actor() {
				continue
			}
			values[key] = string(resolved.Version.Data)
		}
	}
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		if content, ok := values[key]; ok {
			result = append(result, content)
		}
	}
	return result, nil
}

type productionTierSource struct {
	store      *cortex.Cortex
	transcript *activation.SessionTranscript
	sessionID  uuid.UUID
	clock      types.Clock
}

func (source productionTierSource) Entries(
	ctx context.Context,
	request activation.Request,
) ([]activation.Entry, error) {
	if request.SessionID != source.sessionID.String() {
		return nil, fmt.Errorf("operator cognition: activation session scope changed")
	}
	entries, err := source.transcript.Entries(ctx, request)
	if err != nil {
		return nil, err
	}
	now := source.clock.Now()
	for _, memoryType := range memory.Types() {
		for _, id := range source.store.ListByType(memoryType) {
			resolved, err := source.store.Resolve(id)
			if err != nil {
				return nil, err
			}
			if resolved.Head.Actor != request.UserID &&
				resolved.Head.Actor != source.store.Actor() {
				continue
			}
			tier := activation.TierTimeline
			salience := resolved.Head.DeclaredImportance
			switch resolved.Head.Type {
			case memory.Identity, memory.Constraint, memory.Goal:
				tier = activation.TierPinned
				salience = 1
			case memory.Belief:
				tier = activation.TierDreams
			default:
				if now.Sub(resolved.Head.LastUpdatedAt) <= 24*time.Hour {
					tier = activation.TierRecent
				}
			}
			entries = append(entries, activation.Entry{
				Tier: tier, Content: string(resolved.Version.Data),
				Salience: salience,
			})
		}
	}
	return entries, nil
}
