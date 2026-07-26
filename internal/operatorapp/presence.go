package operatorapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/belief/premise"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/curiosity"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/dreamweaver"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/goals"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/temporal"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/memory/forgetting"
	"github.com/paxlabs-inc/ion-agent/internal/presence/automatrix"
	"github.com/paxlabs-inc/ion-agent/internal/presence/heartbeat"
	"github.com/paxlabs-inc/ion-agent/internal/presence/integrity"
	"github.com/paxlabs-inc/ion-agent/internal/security/canary"
	"github.com/paxlabs-inc/ion-agent/internal/security/memoryguard"
	"github.com/paxlabs-inc/ion-agent/internal/security/ssrf"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/internal/swarm"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	presenceStateKind  = "presence_supervisor"
	presenceStateScope = "production"
	presenceIdleAfter  = 5 * time.Minute
)

type scheduleState struct {
	Name        string        `json:"name"`
	Interval    time.Duration `json:"interval"`
	Status      string        `json:"status"`
	LastAttempt *time.Time    `json:"last_attempt,omitempty"`
	LastSuccess *time.Time    `json:"last_success,omitempty"`
	NextDue     time.Time     `json:"next_due"`
	LastError   string        `json:"last_error,omitempty"`
	Summary     string        `json:"summary,omitempty"`
}

type presenceDocument struct {
	Version           int                         `json:"version"`
	StartedAt         time.Time                   `json:"started_at"`
	LastBeat          *time.Time                  `json:"last_beat,omitempty"`
	LastActivity      *time.Time                  `json:"last_activity,omitempty"`
	Schedules         map[string]scheduleState    `json:"schedules"`
	Queue             []automatrix.WorkItem       `json:"queue,omitempty"`
	WorkOwners        map[string]string           `json:"work_owners,omitempty"`
	CuriosityTargets  []curiosity.Target          `json:"curiosity_targets,omitempty"`
	LastForgetting    forgetting.ScanResult       `json:"last_forgetting"`
	LastGoalIDs       []uuid.UUID                 `json:"last_goal_ids,omitempty"`
	LatestIntegrity   *integrity.Report           `json:"latest_integrity,omitempty"`
	Checks            map[string]time.Time        `json:"checks,omitempty"`
	IdleResults       []idleResult                `json:"idle_results,omitempty"`
	MorningBriefs     map[string]morningBrief     `json:"morning_briefs,omitempty"`
	CognitionTriggers map[string]cognitionTrigger `json:"cognition_triggers,omitempty"`
}

type cognitionTrigger struct {
	ActorID string    `json:"actor_id"`
	Cause   string    `json:"cause"`
	At      time.Time `json:"at"`
	NextDue time.Time `json:"next_due"`
}

type morningBrief struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Items       []morningBriefItem `json:"items"`
	Sources     []string           `json:"sources"`
}

type morningBriefItem struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Count   int    `json:"count"`
	Source  string `json:"source"`
}

type idleResult struct {
	ItemID      string    `json:"item_id"`
	ActorID     string    `json:"actor_id"`
	Status      string    `json:"status"`
	Summary     string    `json:"summary"`
	CompletedAt time.Time `json:"completed_at"`
}

type presenceProjection struct {
	Status            string           `json:"status"`
	HeartbeatInterval string           `json:"heartbeat_interval"`
	LastBeat          *time.Time       `json:"last_beat,omitempty"`
	LastActivity      *time.Time       `json:"last_activity,omitempty"`
	Idle              bool             `json:"idle"`
	Schedules         []scheduleState  `json:"schedules"`
	AutomatrixQueued  int              `json:"automatrix_queued"`
	CuriosityTargets  int              `json:"curiosity_targets"`
	LastArchived      int              `json:"last_archived"`
	LastProtected     int              `json:"last_protected"`
	GoalProposals     int              `json:"goal_proposals"`
	Safety            string           `json:"safety_boundary"`
	SinceAway         []continuityItem `json:"since_you_were_away,omitempty"`
	MorningBrief      *morningBrief    `json:"morning_brief,omitempty"`
}

type continuityItem struct {
	Kind       string    `json:"kind"`
	Summary    string    `json:"summary"`
	EvidenceID string    `json:"evidence_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

type presenceSupervisor struct {
	ctx     context.Context
	cancel  context.CancelFunc
	clock   types.Clock
	store   *session.Store
	memory  *cortex.Cortex
	living  *livingContext
	work    *workcontrol.Service
	journal *controlplane.Journal
	emitter controlplane.EventEmitter
	swarm   *swarm.Registry

	queue            *automatrix.Queue
	capturer         *automatrix.Capturer
	protections      *forgetting.ProtectionSet
	forgetting       *forgetting.Scanner
	goals            *goals.Generator
	curiosity        *curiosity.Engine
	dreamweaver      *dreamweaver.Engine
	integrity        *integrity.Generator
	pulse            *heartbeat.Heartbeat
	automatrixRunner *automatrix.Runner
	idleDispatcher   *ssrf.Dispatcher

	cronSignal       chan struct{}
	automatrixSignal chan struct{}
	subagentSignal   chan struct{}
	emotionalSignal  chan struct{}
	dreamSignal      chan struct{}
	reports          chan heartbeat.Beat
	lastActivityUnix atomic.Int64

	mu                   sync.RWMutex
	state                presenceDocument
	lastPersistenceError string
	wg                   sync.WaitGroup
}

func openPresenceSupervisor(
	parent context.Context,
	clock types.Clock,
	config RuntimeConfig,
	store *session.Store,
	memoryStore *cortex.Cortex,
	living *livingContext,
	workService *workcontrol.Service,
	journal *controlplane.Journal,
	emitter controlplane.EventEmitter,
	manager *tools.Manager,
	swarmRegistry *swarm.Registry,
	generator agent.Generator,
	model string,
) (*presenceSupervisor, error) {
	if parent == nil || clock == nil || store == nil || memoryStore == nil ||
		living == nil || workService == nil || journal == nil || emitter == nil || manager == nil ||
		swarmRegistry == nil ||
		generator == nil {
		return nil, fmt.Errorf("operator presence: production dependencies are required")
	}
	ctx, cancel := context.WithCancel(parent)
	supervisor := &presenceSupervisor{
		ctx: ctx, cancel: cancel, clock: clock, store: store, memory: memoryStore,
		living: living, work: workService, journal: journal, emitter: emitter, swarm: swarmRegistry,
		queue:       automatrix.NewQueue(),
		protections: forgetting.NewProtectionSet(),
		cronSignal:  make(chan struct{}, 1), automatrixSignal: make(chan struct{}, 1),
		subagentSignal: make(chan struct{}, 1), emotionalSignal: make(chan struct{}, 1),
		dreamSignal: make(chan struct{}, 1), reports: make(chan heartbeat.Beat, 1),
	}
	if err := supervisor.restore(ctx); err != nil {
		cancel()
		return nil, err
	}
	var err error
	supervisor.capturer, err = automatrix.NewCapturer(supervisor.queue, clock)
	if err != nil {
		cancel()
		return nil, err
	}
	supervisor.idleDispatcher, err = ssrf.New(ssrf.Config{
		AllowedHosts: idleAllowedHosts(config), RequestTimeout: 30 * time.Second,
	})
	if err != nil {
		cancel()
		return nil, err
	}
	supervisor.automatrixRunner, err = automatrix.NewRunner(
		supervisor.queue, manager, supervisor.idleDispatcher,
		automatrix.WithExecutionGuard(supervisor.automatrixExecutionBudget),
	)
	if err != nil {
		cancel()
		supervisor.idleDispatcher.CloseIdleConnections()
		return nil, err
	}
	supervisor.forgetting, err = forgetting.NewScanner(
		memoryStore, supervisor.protections,
		cortexCanaryProtector{store: memoryStore}, clock,
	)
	if err != nil {
		cancel()
		return nil, err
	}
	goalSink, err := goals.NewCortexSink(memoryStore)
	if err != nil {
		cancel()
		return nil, err
	}
	supervisor.goals, err = goals.New(goalSink, clock)
	if err != nil {
		cancel()
		return nil, err
	}
	if strings.TrimSpace(model) != "" && model != "unconfigured" {
		supervisor.curiosity, err = curiosity.New(curiosity.Config{
			Store: memoryStore, Queue: supervisor.queue,
			Scorer: curiosity.ModelEntailmentScorer{
				Provider: generator, Model: model,
			},
			Clock: clock,
		})
		if err != nil {
			cancel()
			return nil, err
		}
		guard, guardErr := memoryguard.New(clock, nil)
		if guardErr != nil {
			cancel()
			return nil, guardErr
		}
		supervisor.dreamweaver, err = dreamweaver.New(dreamweaver.Config{
			Store: memoryStore, Guard: guard,
			Generator: dreamweaver.ModelPatternGenerator{
				Provider: generator, Model: model,
			},
			Clock: clock,
		})
		if err != nil {
			cancel()
			return nil, err
		}
	}
	digestSink, err := integrity.NewJournalSink(
		filepath.Join(config.DataDirectory, "presence", "integrity-digests.jsonl"),
	)
	if err != nil {
		cancel()
		return nil, err
	}
	supervisor.integrity, err = integrity.New(
		[]integrity.Source{controlplaneIntegritySource{journal: journal}},
		digestSink,
	)
	if err != nil {
		cancel()
		return nil, err
	}
	supervisor.pulse, err = heartbeat.New(heartbeat.Signals{
		Cron: supervisor.cronSignal, Automatrix: supervisor.automatrixSignal,
		Subagents: supervisor.subagentSignal, Emotional: supervisor.emotionalSignal,
		Dreamweaver: supervisor.dreamSignal,
	}, supervisor.idle, supervisor.reports)
	if err != nil {
		cancel()
		return nil, err
	}
	supervisor.start()
	return supervisor, nil
}

func idleAllowedHosts(config RuntimeConfig) []string {
	endpoints := []string{config.SearchEndpoint}
	if strings.TrimSpace(config.TavilyAPIKey) != "" {
		endpoints = append([]string{config.TavilySearchEndpoint}, endpoints...)
	}
	hosts := make([]string, 0, len(endpoints))
	seen := map[string]struct{}{}
	for _, endpoint := range endpoints {
		parsed, err := url.Parse(strings.TrimSpace(endpoint))
		if err != nil || !strings.EqualFold(parsed.Scheme, "https") ||
			strings.TrimSpace(parsed.Hostname()) == "" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	if len(hosts) > 0 {
		return hosts
	}
	// This reserved name keeps the SSRF boundary concrete while allowing local
	// GREEN tools to run when no outbound research source is configured.
	return []string{"idle.invalid"}
}

func (supervisor *presenceSupervisor) start() {
	supervisor.wg.Add(7)
	go func() { defer supervisor.wg.Done(); supervisor.pulse.Run(supervisor.ctx) }()
	go supervisor.worker(supervisor.cronSignal, supervisor.runCron)
	go supervisor.worker(supervisor.automatrixSignal, supervisor.runAutomatrix)
	go supervisor.worker(supervisor.subagentSignal, supervisor.runSubagents)
	go supervisor.worker(supervisor.emotionalSignal, func(context.Context) {
		supervisor.recordCheck("emotional_state")
	})
	go supervisor.worker(supervisor.dreamSignal, supervisor.runCognition)
	go func() {
		defer supervisor.wg.Done()
		for {
			select {
			case <-supervisor.ctx.Done():
				return
			case beat := <-supervisor.reports:
				supervisor.recordBeat(beat)
			}
		}
	}()
	// Recover missed schedules immediately instead of waiting up to 60 seconds.
	supervisor.pulse.Pulse(supervisor.clock.Now().UTC())
}

func (supervisor *presenceSupervisor) worker(
	signal <-chan struct{},
	run func(context.Context),
) {
	defer supervisor.wg.Done()
	for {
		select {
		case <-supervisor.ctx.Done():
			return
		case <-signal:
			run(supervisor.ctx)
		}
	}
}

func (supervisor *presenceSupervisor) Close() {
	if supervisor == nil {
		return
	}
	supervisor.cancel()
	supervisor.wg.Wait()
	if supervisor.idleDispatcher != nil {
		supervisor.idleDispatcher.CloseIdleConnections()
	}
}

func (supervisor *presenceSupervisor) Submitted(
	ctx context.Context,
	actorID uuid.UUID,
	_ uuid.UUID,
	content string,
) error {
	now := supervisor.clock.Now().UTC()
	supervisor.lastActivityUnix.Store(now.UnixNano())
	item, err := supervisor.capturer.Detect(
		ctx, content, automatrix.DamageRisk{},
	)
	if err != nil {
		return err
	}
	supervisor.mu.Lock()
	supervisor.state.LastActivity = &now
	if item != nil {
		if supervisor.state.WorkOwners == nil {
			supervisor.state.WorkOwners = make(map[string]string)
		}
		supervisor.state.WorkOwners[item.ID.String()] = actorID.String()
		supervisor.state.Queue = supervisor.queue.Snapshot()
	}
	err = supervisor.saveLocked(ctx)
	supervisor.mu.Unlock()
	if err != nil {
		return err
	}
	normalized := strings.ToLower(content)
	for _, marker := range []string{
		"contradiction", "keeps failing", "failed again", "still unanswered",
	} {
		if strings.Contains(normalized, marker) {
			return supervisor.triggerCognition(ctx, actorID, marker)
		}
	}
	return nil
}

func (supervisor *presenceSupervisor) Completed(
	ctx context.Context,
	actorID uuid.UUID,
	sessionID uuid.UUID,
	content string,
	response agent.Response,
) error {
	now := supervisor.clock.Now().UTC()
	supervisor.lastActivityUnix.Store(now.UnixNano())
	supervisor.mu.Lock()
	supervisor.state.LastActivity = &now
	err := supervisor.saveLocked(ctx)
	supervisor.mu.Unlock()
	if err != nil {
		return err
	}
	normalized := strings.ToLower(response.Content)
	for _, marker := range []string{
		"i don't know", "unable to determine", "not enough evidence",
		"remains unanswered",
	} {
		if !strings.Contains(normalized, marker) {
			continue
		}
		if supervisor.curiosity != nil {
			if _, recordErr := supervisor.curiosity.RecordGap(
				ctx, content, "production response remained unresolved",
				sessionID.String(), nil,
			); recordErr != nil {
				return recordErr
			}
		}
		return supervisor.triggerCognition(ctx, actorID, "unanswered_question")
	}
	return nil
}

func (supervisor *presenceSupervisor) Failed(
	ctx context.Context,
	actorID uuid.UUID,
	_ uuid.UUID,
	content string,
	failureCode string,
) error {
	return supervisor.triggerCognition(
		ctx, actorID,
		"recurring_failure:"+relationshipDomain(content)+":"+safeFailureCode(failureCode),
	)
}

func (supervisor *presenceSupervisor) Recovered(
	ctx context.Context,
	actorID uuid.UUID,
	_ uuid.UUID,
	content string,
	_ string,
	_ agent.Response,
) error {
	return supervisor.triggerCognition(
		ctx, actorID, "verified_repair:"+relationshipDomain(content),
	)
}

func (supervisor *presenceSupervisor) triggerCognition(
	ctx context.Context,
	actorID uuid.UUID,
	cause string,
) error {
	if actorID == uuid.Nil || strings.TrimSpace(cause) == "" {
		return nil
	}
	now := supervisor.clock.Now().UTC()
	key := actorID.String() + "\x00" + strings.TrimSpace(cause)
	supervisor.mu.Lock()
	previous := supervisor.state.CognitionTriggers[key]
	if !previous.At.IsZero() && now.Sub(previous.At) < 6*time.Hour {
		supervisor.mu.Unlock()
		return nil
	}
	schedule := supervisor.state.Schedules["liveness_cognition"]
	if schedule.Status != "running" {
		schedule.NextDue = now
		supervisor.state.Schedules["liveness_cognition"] = schedule
	}
	supervisor.state.CognitionTriggers[key] = cognitionTrigger{
		ActorID: actorID.String(), Cause: cause, At: now, NextDue: now,
	}
	if len(supervisor.state.CognitionTriggers) > 128 {
		var oldestKey string
		var oldest time.Time
		for candidate, trigger := range supervisor.state.CognitionTriggers {
			if oldestKey == "" || trigger.At.Before(oldest) {
				oldestKey, oldest = candidate, trigger.At
			}
		}
		delete(supervisor.state.CognitionTriggers, oldestKey)
	}
	err := supervisor.saveLocked(context.WithoutCancel(ctx))
	supervisor.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case supervisor.dreamSignal <- struct{}{}:
	default:
	}
	return nil
}

func (supervisor *presenceSupervisor) idle() bool {
	nanos := supervisor.lastActivityUnix.Load()
	if nanos == 0 {
		return true
	}
	return supervisor.clock.Now().Sub(time.Unix(0, nanos)) >= presenceIdleAfter
}

func (supervisor *presenceSupervisor) restore(ctx context.Context) error {
	raw, err := supervisor.store.LoadLivingState(
		ctx, presenceStateKind, presenceStateScope,
	)
	now := supervisor.clock.Now().UTC()
	if errors.Is(err, sql.ErrNoRows) {
		supervisor.state = presenceDocument{
			Version: 1, StartedAt: now,
			Schedules:         defaultSchedules(now),
			WorkOwners:        make(map[string]string),
			Checks:            make(map[string]time.Time),
			MorningBriefs:     make(map[string]morningBrief),
			CognitionTriggers: make(map[string]cognitionTrigger),
		}
		return supervisor.save()
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &supervisor.state); err != nil ||
		supervisor.state.Version != 1 || supervisor.state.StartedAt.IsZero() {
		return fmt.Errorf("operator presence: invalid encrypted supervisor state")
	}
	if supervisor.state.Schedules == nil {
		supervisor.state.Schedules = defaultSchedules(now)
	}
	if supervisor.state.WorkOwners == nil {
		supervisor.state.WorkOwners = make(map[string]string)
	}
	if supervisor.state.Checks == nil {
		supervisor.state.Checks = make(map[string]time.Time)
	}
	if supervisor.state.MorningBriefs == nil {
		supervisor.state.MorningBriefs = make(map[string]morningBrief)
	}
	if supervisor.state.CognitionTriggers == nil {
		supervisor.state.CognitionTriggers = make(map[string]cognitionTrigger)
	}
	recoveredInterrupted := false
	for key, schedule := range supervisor.state.Schedules {
		if schedule.Status != "running" {
			continue
		}
		schedule.Status = "degraded"
		schedule.LastError = "interrupted by daemon restart; retry is due"
		schedule.NextDue = now
		supervisor.state.Schedules[key] = schedule
		recoveredInterrupted = true
	}
	if recoveredInterrupted {
		if err := supervisor.saveLocked(ctx); err != nil {
			return fmt.Errorf(
				"operator presence: persist interrupted schedule recovery: %w",
				err,
			)
		}
	}
	for _, item := range supervisor.state.Queue {
		if err := supervisor.queue.Enqueue(ctx, item); err != nil {
			return fmt.Errorf("operator presence: restore Automatrix queue: %w", err)
		}
	}
	if supervisor.state.LastActivity != nil {
		supervisor.lastActivityUnix.Store(supervisor.state.LastActivity.UnixNano())
	}
	return nil
}

func defaultSchedules(now time.Time) map[string]scheduleState {
	return map[string]scheduleState{
		"strategic_forgetting": {
			Name: "Strategic forgetting", Interval: 24 * time.Hour,
			Status: "scheduled", NextDue: now.Add(24 * time.Hour),
		},
		"liveness_cognition": {
			Name:     "Curiosity, Dreamweaver, and goal proposals",
			Interval: 24 * time.Hour, Status: "scheduled",
			NextDue: now.Add(24 * time.Hour),
		},
		"weekly_integrity": {
			Name: "Weekly integrity digest", Interval: 7 * 24 * time.Hour,
			Status: "scheduled", NextDue: now.Add(7 * 24 * time.Hour),
		},
		"morning_brief": {
			Name: "Morning brief", Interval: 24 * time.Hour,
			Status: "scheduled", NextDue: now.Add(24 * time.Hour),
			Summary: "Uses allowlisted control-plane, goal, and explicit-deadline records.",
		},
	}
}

func (supervisor *presenceSupervisor) runCron(ctx context.Context) {
	supervisor.recordCheck("cron")
	if err := supervisor.runScheduled(
		ctx, "strategic_forgetting", supervisor.runForgetting,
	); err != nil {
		supervisor.recordRuntimeError(err)
	}
	if err := supervisor.runScheduled(
		ctx, "weekly_integrity", supervisor.runIntegrity,
	); err != nil {
		supervisor.recordRuntimeError(err)
	}
	if err := supervisor.runScheduled(
		ctx, "morning_brief", supervisor.runMorningBrief,
	); err != nil {
		supervisor.recordRuntimeError(err)
	}
}

func (supervisor *presenceSupervisor) runAutomatrix(ctx context.Context) {
	supervisor.recordCheck("automatrix")
	result, err := supervisor.automatrixRunner.RunCycle(ctx)
	if err != nil {
		supervisor.recordRuntimeError(err)
		return
	}
	if len(result.Outcomes) == 0 {
		return
	}
	now := supervisor.clock.Now().UTC()
	type aggregate struct {
		calls  int
		failed int
	}
	aggregates := make(map[string]aggregate)
	for _, outcome := range result.Outcomes {
		item := aggregates[outcome.ItemID]
		item.calls++
		if outcome.Err != nil {
			item.failed++
		}
		aggregates[outcome.ItemID] = item
	}
	remaining := make(map[string]struct{})
	for _, item := range supervisor.queue.Snapshot() {
		remaining[item.ID.String()] = struct{}{}
	}
	type completedEvent struct {
		actor uuid.UUID
		item  idleResult
	}
	var events []completedEvent
	supervisor.mu.Lock()
	for itemID, aggregate := range aggregates {
		owner := supervisor.state.WorkOwners[itemID]
		status := "completed"
		if _, exists := remaining[itemID]; exists {
			status = "partial"
		} else {
			delete(supervisor.state.WorkOwners, itemID)
		}
		if aggregate.failed > 0 {
			status = "completed_with_denials"
		}
		item := idleResult{
			ItemID: itemID, ActorID: owner, Status: status,
			Summary: fmt.Sprintf(
				"%d bounded actions attempted; %d denied or failed",
				aggregate.calls, aggregate.failed,
			),
			CompletedAt: now,
		}
		supervisor.state.IdleResults = append(supervisor.state.IdleResults, item)
		if actorID, parseErr := uuid.Parse(owner); parseErr == nil {
			events = append(events, completedEvent{actor: actorID, item: item})
		}
	}
	if len(supervisor.state.IdleResults) > 64 {
		supervisor.state.IdleResults = append(
			[]idleResult(nil), supervisor.state.IdleResults[len(supervisor.state.IdleResults)-64:]...,
		)
	}
	supervisor.state.Queue = supervisor.queue.Snapshot()
	saveErr := supervisor.saveLocked(context.WithoutCancel(ctx))
	supervisor.mu.Unlock()
	if saveErr != nil {
		supervisor.recordRuntimeError(saveErr)
		return
	}
	for _, event := range events {
		raw, marshalErr := json.Marshal(event.item)
		if marshalErr != nil {
			continue
		}
		_, _ = supervisor.emitter.Emit(
			context.WithoutCancel(ctx), controlplane.EventAutomatrixCompleted,
			controlplane.Correlation{ActorID: event.actor}, raw,
		)
	}
}

func (supervisor *presenceSupervisor) runSubagents(ctx context.Context) {
	supervisor.recordCheck("subagents")
	for {
		select {
		case <-ctx.Done():
			return
		case completion := <-supervisor.swarm.Completions():
			sessionID, err := uuid.Parse(completion.SessionID)
			if err != nil {
				supervisor.recordRuntimeError(err)
				continue
			}
			actorID, err := supervisor.living.Owner(ctx, sessionID)
			if err != nil {
				supervisor.recordRuntimeError(err)
				continue
			}
			payload, err := json.Marshal(map[string]any{
				"kind": "subagent_completion", "agent_id": completion.AgentID,
				"actor_id": actorID, "session_id": sessionID,
				"state": completion.State, "artifact": completion.Artifact,
				"has_error":      strings.TrimSpace(completion.Error) != "",
				"synthesized_at": supervisor.clock.Now().UTC(),
			})
			if err != nil {
				supervisor.recordRuntimeError(err)
				continue
			}
			if _, err := supervisor.memory.Write(
				ctx, memory.Pattern, payload, "subagent-synthesis",
			); err != nil {
				supervisor.recordRuntimeError(err)
				continue
			}
			now := supervisor.clock.Now().UTC()
			status := string(completion.State)
			summary := "A bounded sub-agent result was synthesized into encrypted memory."
			if strings.TrimSpace(completion.Error) != "" {
				summary = "A bounded sub-agent stopped with an error; its partial artifact was preserved."
			}
			item := idleResult{
				ItemID:  "subagent:" + completion.AgentID,
				ActorID: actorID.String(), Status: status,
				Summary: summary, CompletedAt: now,
			}
			supervisor.mu.Lock()
			supervisor.state.IdleResults = append(supervisor.state.IdleResults, item)
			if len(supervisor.state.IdleResults) > 64 {
				supervisor.state.IdleResults = append(
					[]idleResult(nil), supervisor.state.IdleResults[len(supervisor.state.IdleResults)-64:]...,
				)
			}
			saveErr := supervisor.saveLocked(context.WithoutCancel(ctx))
			supervisor.mu.Unlock()
			if saveErr != nil {
				supervisor.recordRuntimeError(saveErr)
				continue
			}
			raw, _ := json.Marshal(item)
			_, _ = supervisor.emitter.Emit(
				context.WithoutCancel(ctx), controlplane.EventAgentCompleted,
				controlplane.Correlation{
					ActorID: actorID, SessionID: &sessionID,
				}, raw,
			)
		default:
			return
		}
	}
}

func (supervisor *presenceSupervisor) runCognition(ctx context.Context) {
	supervisor.recordCheck("dreamweaver")
	if err := supervisor.runScheduled(
		ctx, "liveness_cognition", supervisor.runLivenessCognition,
	); err != nil {
		supervisor.recordRuntimeError(err)
	}
}

func (supervisor *presenceSupervisor) runScheduled(
	ctx context.Context,
	name string,
	run func(context.Context) (string, error),
) error {
	now := supervisor.clock.Now().UTC()
	supervisor.mu.Lock()
	schedule, exists := supervisor.state.Schedules[name]
	if !exists || schedule.Status == "running" || schedule.NextDue.After(now) {
		supervisor.mu.Unlock()
		return nil
	}
	schedule.Status = "running"
	schedule.LastAttempt = &now
	schedule.NextDue = now.Add(schedule.Interval)
	supervisor.state.Schedules[name] = schedule
	if err := supervisor.saveLocked(context.WithoutCancel(ctx)); err != nil {
		schedule.Status = "degraded"
		schedule.LastError = safePresenceError(fmt.Errorf(
			"persist running schedule: %w", err,
		))
		schedule.NextDue = now
		supervisor.state.Schedules[name] = schedule
		supervisor.lastPersistenceError = schedule.LastError
		supervisor.mu.Unlock()
		return err
	}
	supervisor.mu.Unlock()

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	summary, err := run(runCtx)
	cancel()
	finished := supervisor.clock.Now().UTC()
	supervisor.mu.Lock()
	schedule = supervisor.state.Schedules[name]
	if err != nil {
		schedule.Status = "degraded"
		schedule.LastError = safePresenceError(err)
		retry := 5 * time.Minute
		if schedule.Interval < retry {
			retry = schedule.Interval
		}
		schedule.NextDue = finished.Add(retry)
	} else {
		schedule.Status = "ready"
		schedule.LastSuccess = &finished
		schedule.LastError = ""
		schedule.Summary = summary
		schedule.NextDue = finished.Add(schedule.Interval)
	}
	supervisor.state.Schedules[name] = schedule
	if saveErr := supervisor.saveLocked(context.WithoutCancel(ctx)); saveErr != nil {
		schedule.Status = "degraded"
		schedule.LastError = safePresenceError(fmt.Errorf(
			"persist completed schedule: %w", saveErr,
		))
		schedule.NextDue = finished
		supervisor.state.Schedules[name] = schedule
		supervisor.lastPersistenceError = schedule.LastError
		supervisor.mu.Unlock()
		return saveErr
	}
	supervisor.lastPersistenceError = ""
	supervisor.mu.Unlock()
	return err
}

func (supervisor *presenceSupervisor) runForgetting(
	ctx context.Context,
) (string, error) {
	if err := supervisor.refreshProtections(ctx); err != nil {
		return "", err
	}
	result, err := supervisor.forgetting.Scan(ctx)
	if err != nil {
		return "", err
	}
	supervisor.mu.Lock()
	supervisor.state.LastForgetting = result
	supervisor.mu.Unlock()
	return fmt.Sprintf(
		"archived %d; protected %d; canaries blocked %d",
		len(result.Archived), len(result.Protected), len(result.Canaries),
	), nil
}

func (supervisor *presenceSupervisor) refreshProtections(ctx context.Context) error {
	sessions, err := supervisor.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	var active []uuid.UUID
	for _, found := range sessions {
		raw, err := supervisor.store.LoadCognitionState(ctx, found.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		var snapshot cognitionSnapshot
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return err
		}
		for _, item := range snapshot.Premises.Items {
			if item != nil && item.Status != premise.Refuted && item.MemoryID != nil {
				active = append(active, *item.MemoryID)
			}
		}
	}
	supervisor.protections.ReplaceLoadBearing(active)
	return nil
}

func (supervisor *presenceSupervisor) runLivenessCognition(
	ctx context.Context,
) (string, error) {
	var targets []curiosity.Target
	var runErrors []error
	if supervisor.curiosity != nil {
		found, err := supervisor.curiosity.Scan(ctx)
		if err != nil {
			runErrors = append(runErrors, err)
		} else {
			targets = found
		}
	}
	if supervisor.dreamweaver != nil {
		if _, err := supervisor.dreamweaver.RunCycle(ctx); err != nil {
			runErrors = append(runErrors, err)
		}
	}
	proposals, err := supervisor.goals.Generate(ctx, goals.Inputs{
		Curiosity:     targets,
		Dreams:        supervisor.dreamInputs(),
		Relationships: supervisor.living.relationshipSnapshots(),
	})
	if err != nil {
		runErrors = append(runErrors, err)
	}
	ids := make([]uuid.UUID, 0, len(proposals))
	for _, proposal := range proposals {
		ids = append(ids, proposal.ID)
	}
	supervisor.mu.Lock()
	supervisor.state.CuriosityTargets = targets
	supervisor.state.LastGoalIDs = ids
	supervisor.state.Queue = supervisor.queue.Snapshot()
	supervisor.mu.Unlock()
	summary := fmt.Sprintf(
		"%d curiosity targets; %d user-approval-only goal proposals",
		len(targets), len(proposals),
	)
	return summary, errors.Join(runErrors...)
}

func (supervisor *presenceSupervisor) dreamInputs() []goals.DreamInput {
	var result []goals.DreamInput
	for _, id := range supervisor.memory.ListByType(memory.Belief) {
		resolved, err := supervisor.memory.Resolve(id)
		if err != nil {
			continue
		}
		var belief dreamweaver.DerivedBelief
		if json.Unmarshal(resolved.Version.Data, &belief) == nil &&
			belief.DreamDerived {
			result = append(result, goals.DreamInput{ID: id, Belief: belief})
		}
	}
	return result
}

func (supervisor *presenceSupervisor) runIntegrity(
	ctx context.Context,
) (string, error) {
	report, err := supervisor.integrity.Run(ctx, supervisor.clock.Now().UTC())
	if err != nil {
		return "", err
	}
	supervisor.mu.Lock()
	supervisor.state.LatestIntegrity = &report
	supervisor.mu.Unlock()
	return fmt.Sprintf(
		"verified digest %s with %d recorded changes",
		shortDigest(report.Digest), len(report.Changes),
	), nil
}

func (supervisor *presenceSupervisor) runMorningBrief(
	ctx context.Context,
) (string, error) {
	now := supervisor.clock.Now().UTC()
	actors := make(map[uuid.UUID]struct{})
	for _, snapshot := range supervisor.living.relationshipSnapshots() {
		if actorID, err := uuid.Parse(snapshot.UserID); err == nil {
			actors[actorID] = struct{}{}
		}
	}
	briefs := make(map[string]morningBrief, len(actors))
	for actorID := range actors {
		replay, err := supervisor.journal.ReplayActor(ctx, actorID, 0, 2000)
		if err != nil {
			return "", err
		}
		counts := make(map[string]int)
		for _, event := range replay.Events {
			if event.OccurredAt.Before(now.Add(-24*time.Hour)) ||
				event.OccurredAt.After(now) {
				continue
			}
			switch event.Type {
			case controlplane.EventTurnCompleted:
				counts["Work completed"]++
			case controlplane.EventPredictionMismatched:
				counts["Contradictions discovered"]++
			case controlplane.EventDreamweaverDerived:
				counts["Connections derived"]++
			case controlplane.EventCuriosityTargeted:
				counts["Questions investigated"]++
			case controlplane.EventRepairLearned:
				counts["Failures repaired"]++
			case controlplane.EventApprovalRequested:
				counts["Decisions awaiting approval"]++
			case controlplane.EventTurnIncomplete, controlplane.EventTurnFailed:
				counts["Work still incomplete"]++
			}
		}
		goalCount := supervisor.goalCount(actorID)
		if goalCount > 0 {
			counts["Goal proposals to review"] = goalCount
		}
		deadlineCount, err := supervisor.deadlineCount(ctx, actorID, now)
		if err != nil {
			return "", err
		}
		if deadlineCount > 0 {
			counts["Upcoming explicit deadlines"] = deadlineCount
		}
		labels := make([]string, 0, len(counts))
		for label := range counts {
			labels = append(labels, label)
		}
		sort.Strings(labels)
		items := make([]morningBriefItem, 0, len(labels))
		for _, label := range labels {
			count := counts[label]
			items = append(items, morningBriefItem{
				Kind: label, Count: count,
				Summary: fmt.Sprintf("%d verified %s", count, strings.ToLower(label)),
				Source:  "allowlisted durable records",
			})
		}
		if len(items) == 0 {
			items = append(items, morningBriefItem{
				Kind: "No recorded activity", Count: 0,
				Summary: "No verified work, discovery, repair, proposal, or deadline activity was recorded in this period.",
				Source:  "allowlisted durable records",
			})
		}
		briefs[actorID.String()] = morningBrief{
			GeneratedAt: now, Items: items,
			Sources: []string{
				"actor-scoped control-plane events", "encrypted goal memories",
				"explicit temporal deadlines",
			},
		}
	}
	supervisor.mu.Lock()
	for actor, brief := range briefs {
		supervisor.state.MorningBriefs[actor] = brief
	}
	supervisor.mu.Unlock()
	return fmt.Sprintf("generated %d actor-scoped briefs from allowlisted durable records", len(briefs)), nil
}

func (supervisor *presenceSupervisor) goalCount(actorID uuid.UUID) int {
	count := 0
	for _, id := range supervisor.memory.ListByType(memory.Goal) {
		resolved, err := supervisor.memory.Resolve(id)
		if err == nil && resolved.Head.Actor == actorID.String() {
			count++
		}
	}
	return count
}

func (supervisor *presenceSupervisor) deadlineCount(
	ctx context.Context,
	actorID uuid.UUID,
	now time.Time,
) (int, error) {
	states, err := supervisor.store.ListLivingStates(ctx, temporalStateKind)
	if err != nil {
		return 0, err
	}
	prefix := actorID.String() + "\x00"
	count := 0
	for _, stored := range states {
		if !strings.HasPrefix(stored.Scope, prefix) {
			continue
		}
		var state temporal.State
		if err := json.Unmarshal(stored.State, &state); err != nil {
			return 0, err
		}
		if state.HasDeadline && !state.Deadline.Before(now) {
			count++
		}
	}
	return count, nil
}

func (supervisor *presenceSupervisor) recordBeat(beat heartbeat.Beat) {
	at := beat.At.UTC()
	supervisor.mu.Lock()
	supervisor.state.LastBeat = &at
	if err := supervisor.saveLocked(context.WithoutCancel(supervisor.ctx)); err != nil {
		supervisor.lastPersistenceError = safePresenceError(err)
	}
	supervisor.mu.Unlock()
}

func (supervisor *presenceSupervisor) recordCheck(name string) {
	now := supervisor.clock.Now().UTC()
	supervisor.mu.Lock()
	supervisor.state.Checks[name] = now
	if err := supervisor.saveLocked(context.WithoutCancel(supervisor.ctx)); err != nil {
		supervisor.lastPersistenceError = safePresenceError(err)
	}
	supervisor.mu.Unlock()
}

func (supervisor *presenceSupervisor) save() error {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	return supervisor.saveLocked(context.Background())
}

func (supervisor *presenceSupervisor) saveLocked(ctx context.Context) error {
	raw, err := json.Marshal(supervisor.state)
	if err != nil {
		return err
	}
	return supervisor.store.SaveLivingState(
		ctx, presenceStateKind, presenceStateScope, raw,
	)
}

func (supervisor *presenceSupervisor) Projection(
	actorID uuid.UUID,
) presenceProjection {
	supervisor.mu.RLock()
	state := supervisor.state
	state.Schedules = cloneSchedules(state.Schedules)
	persistenceError := supervisor.lastPersistenceError
	supervisor.mu.RUnlock()
	schedules := orderedSchedules(state.Schedules)
	queued := len(supervisor.Automatrix(actorID))
	var actorBrief *morningBrief
	if brief, exists := state.MorningBriefs[actorID.String()]; exists {
		copy := brief
		copy.Items = append([]morningBriefItem(nil), brief.Items...)
		copy.Sources = append([]string(nil), brief.Sources...)
		actorBrief = &copy
	}
	status := "ready"
	if persistenceError != "" {
		status = "degraded"
	}
	return presenceProjection{
		Status: status, HeartbeatInterval: heartbeat.Interval.String(),
		LastBeat: state.LastBeat, LastActivity: state.LastActivity,
		Idle: supervisor.idle(), Schedules: schedules,
		AutomatrixQueued: queued,
		CuriosityTargets: len(state.CuriosityTargets),
		LastArchived:     len(state.LastForgetting.Archived),
		LastProtected:    len(state.LastForgetting.Protected),
		GoalProposals:    len(state.LastGoalIDs),
		Safety:           "background work cannot move value, communicate externally, or bypass policy",
		SinceAway:        supervisor.continuity(actorID),
		MorningBrief:     actorBrief,
	}
}

func (supervisor *presenceSupervisor) continuity(actorID uuid.UUID) []continuityItem {
	results := supervisor.IdleResults(actorID)
	if len(results) > 10 {
		results = results[len(results)-10:]
	}
	items := make([]continuityItem, 0, len(results))
	for _, result := range results {
		kind := "I learned"
		if strings.HasPrefix(result.ItemID, "subagent:") {
			kind = "I preserved"
		}
		items = append(items, continuityItem{
			Kind: kind, Summary: result.Summary,
			EvidenceID: result.ItemID, OccurredAt: result.CompletedAt,
		})
	}
	return items
}

func (supervisor *presenceSupervisor) Schedules() []scheduleState {
	supervisor.mu.RLock()
	defer supervisor.mu.RUnlock()
	return orderedSchedules(supervisor.state.Schedules)
}

func (supervisor *presenceSupervisor) Automatrix(
	actorID uuid.UUID,
) []automatrix.WorkItem {
	if actorID == uuid.Nil {
		return nil
	}
	supervisor.mu.RLock()
	owners := make(map[string]string, len(supervisor.state.WorkOwners))
	for id, owner := range supervisor.state.WorkOwners {
		owners[id] = owner
	}
	supervisor.mu.RUnlock()
	var result []automatrix.WorkItem
	for _, item := range supervisor.queue.Snapshot() {
		if owners[item.ID.String()] == actorID.String() {
			result = append(result, item)
		}
	}
	return result
}

func (supervisor *presenceSupervisor) automatrixExecutionBudget(
	ctx context.Context,
	item automatrix.WorkItem,
) (automatrix.ExecutionBudget, error) {
	supervisor.mu.RLock()
	owner := supervisor.state.WorkOwners[item.ID.String()]
	var lastCompleted time.Time
	for _, result := range supervisor.state.IdleResults {
		if result.ActorID == owner && result.CompletedAt.After(lastCompleted) {
			lastCompleted = result.CompletedAt
		}
	}
	supervisor.mu.RUnlock()
	actorID, err := uuid.Parse(owner)
	if err != nil {
		return automatrix.ExecutionBudget{}, fmt.Errorf("operator presence: unattended work owner is invalid")
	}
	portfolio, err := supervisor.work.Get(ctx, actorID)
	if err != nil {
		return automatrix.ExecutionBudget{}, err
	}
	settings := portfolio.Autonomy
	if settings.Mode != workcontrol.AutonomyApproved || settings.Paused {
		return automatrix.ExecutionBudget{Allowed: false}, nil
	}
	if !lastCompleted.IsZero() && settings.CooldownSecond > 0 &&
		supervisor.clock.Now().UTC().Before(lastCompleted.Add(time.Duration(settings.CooldownSecond)*time.Second)) {
		return automatrix.ExecutionBudget{Allowed: false}, nil
	}
	maxCalls := settings.MaxToolCalls
	if maxCalls > automatrix.MaxToolCallsPerCycle {
		maxCalls = automatrix.MaxToolCallsPerCycle
	}
	return automatrix.ExecutionBudget{
		Allowed: true, MaxCalls: maxCalls, MaxErrors: settings.MaxErrors,
		MaxElapsed: time.Duration(settings.MaxElapsedSecond) * time.Second,
	}, nil
}

func (supervisor *presenceSupervisor) ApproveAutomatrix(
	ctx context.Context,
	actorID uuid.UUID,
	itemID uuid.UUID,
	actions []automatrix.Action,
) (automatrix.WorkItem, error) {
	if actorID == uuid.Nil || itemID == uuid.Nil {
		return automatrix.WorkItem{}, fmt.Errorf("operator presence: authenticated work scope is required")
	}
	portfolio, err := supervisor.work.Get(ctx, actorID)
	if err != nil {
		return automatrix.WorkItem{}, err
	}
	if portfolio.Autonomy.Mode != workcontrol.AutonomyApproved || portfolio.Autonomy.Paused {
		return automatrix.WorkItem{}, fmt.Errorf("operator presence: enable approved autonomy and resume before approving background execution")
	}
	supervisor.mu.Lock()
	if supervisor.state.WorkOwners[itemID.String()] != actorID.String() {
		supervisor.mu.Unlock()
		return automatrix.WorkItem{}, fmt.Errorf("operator presence: work item is outside authenticated scope")
	}
	supervisor.mu.Unlock()
	item, err := supervisor.queue.Approve(
		ctx, itemID, actions, supervisor.clock.Now().UTC(),
	)
	if err != nil {
		return automatrix.WorkItem{}, err
	}
	supervisor.mu.Lock()
	supervisor.state.Queue = supervisor.queue.Snapshot()
	err = supervisor.saveLocked(ctx)
	supervisor.mu.Unlock()
	if err != nil {
		return automatrix.WorkItem{}, err
	}
	return item, nil
}

func (supervisor *presenceSupervisor) RejectAutomatrix(
	ctx context.Context,
	actorID uuid.UUID,
	itemID uuid.UUID,
) error {
	if actorID == uuid.Nil || itemID == uuid.Nil {
		return fmt.Errorf("operator presence: authenticated work scope is required")
	}
	supervisor.mu.Lock()
	if supervisor.state.WorkOwners[itemID.String()] != actorID.String() {
		supervisor.mu.Unlock()
		return fmt.Errorf("operator presence: work item is outside authenticated scope")
	}
	supervisor.mu.Unlock()
	if err := supervisor.queue.Reject(ctx, itemID); err != nil {
		return err
	}
	supervisor.mu.Lock()
	delete(supervisor.state.WorkOwners, itemID.String())
	supervisor.state.Queue = supervisor.queue.Snapshot()
	err := supervisor.saveLocked(ctx)
	supervisor.mu.Unlock()
	return err
}

func (supervisor *presenceSupervisor) IdleResults(actorID uuid.UUID) []idleResult {
	if actorID == uuid.Nil {
		return nil
	}
	supervisor.mu.RLock()
	defer supervisor.mu.RUnlock()
	var result []idleResult
	for _, item := range supervisor.state.IdleResults {
		if item.ActorID == actorID.String() {
			result = append(result, item)
		}
	}
	return result
}

func (supervisor *presenceSupervisor) Curiosity() []curiosity.Target {
	supervisor.mu.RLock()
	defer supervisor.mu.RUnlock()
	return append([]curiosity.Target(nil), supervisor.state.CuriosityTargets...)
}

func (supervisor *presenceSupervisor) LatestIntegrity() any {
	supervisor.mu.RLock()
	defer supervisor.mu.RUnlock()
	if supervisor.state.LatestIntegrity == nil {
		return map[string]any{
			"status": "scheduled", "verification": "BLAKE3 canonical digest",
		}
	}
	report := *supervisor.state.LatestIntegrity
	report.Changes = append([]integrity.Change(nil), report.Changes...)
	return map[string]any{
		"status": "ready", "report": report,
		"verified": integrity.Verify(report),
	}
}

func (supervisor *presenceSupervisor) RunNow(
	ctx context.Context,
	name string,
) (scheduleState, error) {
	name = strings.TrimSpace(name)
	switch name {
	case "strategic_forgetting", "liveness_cognition", "weekly_integrity", "morning_brief":
	default:
		return scheduleState{}, fmt.Errorf("operator presence: unknown runnable schedule")
	}
	supervisor.mu.Lock()
	schedule := supervisor.state.Schedules[name]
	schedule.NextDue = supervisor.clock.Now().UTC()
	if schedule.Status == "running" {
		supervisor.mu.Unlock()
		return schedule, fmt.Errorf("operator presence: schedule is already running")
	}
	supervisor.state.Schedules[name] = schedule
	supervisor.mu.Unlock()
	var err error
	switch name {
	case "strategic_forgetting":
		err = supervisor.runScheduled(ctx, name, supervisor.runForgetting)
	case "liveness_cognition":
		err = supervisor.runScheduled(ctx, name, supervisor.runLivenessCognition)
	case "weekly_integrity":
		err = supervisor.runScheduled(ctx, name, supervisor.runIntegrity)
	case "morning_brief":
		err = supervisor.runScheduled(ctx, name, supervisor.runMorningBrief)
	}
	if err != nil {
		return scheduleState{}, err
	}
	supervisor.mu.RLock()
	result := supervisor.state.Schedules[name]
	supervisor.mu.RUnlock()
	if result.Status == "degraded" {
		return result, fmt.Errorf("operator presence: %s", result.LastError)
	}
	return result, nil
}

func (supervisor *presenceSupervisor) recordRuntimeError(err error) {
	if err == nil {
		return
	}
	supervisor.mu.Lock()
	supervisor.lastPersistenceError = safePresenceError(err)
	supervisor.mu.Unlock()
}

func cloneSchedules(source map[string]scheduleState) map[string]scheduleState {
	result := make(map[string]scheduleState, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func orderedSchedules(source map[string]scheduleState) []scheduleState {
	result := make([]scheduleState, 0, len(source)+1)
	result = append(result, scheduleState{
		Name: "Presence heartbeat", Interval: heartbeat.Interval,
		Status: "ready", NextDue: time.Time{},
		Summary: "Checks schedules, Automatrix, subagents, emotion, and Dreamweaver without blocking.",
	})
	for _, schedule := range source {
		result = append(result, schedule)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

type cortexCanaryProtector struct{ store *cortex.Cortex }

func (protector cortexCanaryProtector) ProtectArchive(
	id string,
	_ string,
) error {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	resolved, err := protector.store.Resolve(parsed)
	if err != nil {
		return err
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(resolved.Version.Data, &document) != nil {
		return nil
	}
	for _, field := range []string{"canary", "honeypot"} {
		var marked bool
		if json.Unmarshal(document[field], &marked) == nil && marked {
			return canary.ErrCanaryArchive
		}
	}
	return nil
}

type controlplaneIntegritySource struct{ journal *controlplane.Journal }

func (source controlplaneIntegritySource) Changes(
	ctx context.Context,
	from time.Time,
	until time.Time,
) ([]integrity.Change, error) {
	var result []integrity.Change
	var cursor uint64
	for {
		replay, err := source.journal.Replay(ctx, cursor, 1000)
		if err != nil {
			return nil, err
		}
		if replay.Gap {
			cursor = replay.Earliest - 1
			continue
		}
		for _, event := range replay.Events {
			cursor = event.Sequence
			if event.OccurredAt.Before(from) || event.OccurredAt.After(until) {
				continue
			}
			var category integrity.Category
			var summary string
			switch event.Type {
			case controlplane.EventEmotionalChanged:
				category, summary = integrity.EmotionalChange, "durable emotional state changed"
			case controlplane.EventRelationshipChanged:
				category, summary = integrity.TrustChange, "durable relationship state changed"
			default:
				continue
			}
			evidence, _ := json.Marshal(map[string]any{
				"event_id": event.EventID, "sequence": event.Sequence,
				"type": event.Type,
			})
			result = append(result, integrity.Change{
				Category: category, At: event.OccurredAt,
				Summary: summary, Evidence: evidence,
			})
		}
		if cursor >= replay.Latest || len(replay.Events) == 0 {
			break
		}
	}
	return result, nil
}

func safePresenceError(err error) string {
	value := strings.Join(strings.Fields(err.Error()), " ")
	if len(value) > 240 {
		value = value[:240]
	}
	return value
}

func shortDigest(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
