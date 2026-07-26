package presence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/presence/automatrix"
	"github.com/paxlabs-inc/ion-agent/internal/presence/gateway"
	"github.com/paxlabs-inc/ion-agent/internal/presence/heartbeat"
	"github.com/paxlabs-inc/ion-agent/internal/presence/identity"
	"github.com/paxlabs-inc/ion-agent/internal/presence/integrity"
	"github.com/paxlabs-inc/ion-agent/internal/presence/morning"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func TestHeartbeatAcceptanceExactCadenceFiveChecksAndNonBlocking(t *testing.T) {
	channels := []chan struct{}{
		make(chan struct{}, 2), make(chan struct{}, 2), make(chan struct{}, 2),
		make(chan struct{}, 2), make(chan struct{}, 2),
	}
	reports := make(chan heartbeat.Beat, 2)
	var idle atomic.Bool
	idle.Store(true)
	pulse, err := heartbeat.New(heartbeat.Signals{
		Cron: channels[0], Automatrix: channels[1], Subagents: channels[2],
		Emotional: channels[3], Dreamweaver: channels[4],
	}, idle.Load, reports)
	if err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		pulse.RunTicks(ctx, ticks)
	}()
	first := time.Unix(1_000, 0)
	ticks <- first
	ticks <- first.Add(heartbeat.Interval)
	close(ticks)
	<-done
	firstBeat, secondBeat := <-reports, <-reports
	if secondBeat.At.Sub(firstBeat.At) != 60*time.Second {
		t.Fatalf("heartbeat cadence = %s", secondBeat.At.Sub(firstBeat.At))
	}
	for index, channel := range channels {
		if len(channel) != 2 {
			t.Fatalf("subsystem %d checks = %d, want 2", index, len(channel))
		}
	}

	// Every queue is now full. A pulse must skip all five without waiting.
	start := time.Now()
	saturated := pulse.Pulse(first.Add(2 * heartbeat.Interval))
	elapsed := time.Since(start)
	if elapsed >= time.Millisecond {
		t.Fatalf("saturated heartbeat took %s", elapsed)
	}
	for check, accepted := range saturated.Accepted {
		if accepted {
			t.Fatalf("saturated %s check unexpectedly queued", check)
		}
	}
}

type briefSource struct {
	items []morning.Item
}

type briefSink struct {
	delivered []morning.Brief
}

func (sink *briefSink) Deliver(_ context.Context, brief morning.Brief) error {
	sink.delivered = append(sink.delivered, brief)
	return nil
}

func (source briefSource) Collect(
	context.Context,
	time.Time,
	time.Time,
) ([]morning.Item, error) {
	return append([]morning.Item(nil), source.items...), nil
}

func TestMorningBriefAcceptanceRealTypedDataAndPositiveAllowlist(t *testing.T) {
	ctx := context.Background()
	manager := presenceManager(t)
	var searchCalls, recallCalls, valueCalls atomic.Int32
	register := func(name string, class tools.Classification, calls *atomic.Int32) {
		t.Helper()
		if err := manager.Register(ctx, tools.Registration{
			Name: name, Description: name,
			Parameters:     json.RawMessage(`{"type":"object"}`),
			Classification: class,
			Check:          func(context.Context) error { return nil },
			Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				calls.Add(1)
				return json.RawMessage(`{"observed":true}`), nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	register("web_search", tools.ClassificationGreen, &searchCalls)
	register("memory_recall", tools.ClassificationGreen, &recallCalls)
	register("send_payment", tools.ClassificationRed, &valueCalls)

	now := time.Date(2026, 7, 19, 7, 30, 0, 0, time.UTC)
	source := briefSource{items: []morning.Item{
		{Kind: morning.PRMerged, Title: "Merge safety rail", Source: "github", At: now.Add(-6 * time.Hour)},
		{Kind: morning.IssueOpened, Title: "Investigate latency", Source: "github", At: now.Add(-5 * time.Hour)},
		{Kind: morning.DeploySucceeded, Title: "staging", Source: "deploy", At: now.Add(-4 * time.Hour)},
		{Kind: morning.CalendarEvent, Title: "Design review", Source: "calendar", At: now.Add(-3 * time.Hour)},
	}}
	generator, err := morning.NewGenerator([]morning.Source{source}, manager)
	if err != nil {
		t.Fatal(err)
	}
	profile := morning.Profile{
		Name: "owner", Hour: 8, Minute: 15, Location: time.UTC,
		SearchQueries: []string{"overnight project changes"},
		RecallQueries: []string{"last user priority"},
	}
	sink := &briefSink{}
	service, err := morning.NewService(generator, sink)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := service.RunOnce(ctx, profile, now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"PRs merged (1)", "Issues opened (1)",
		"Deploys succeeded (1)", "Calendar events (1)",
	} {
		if !strings.Contains(brief.Text, fragment) {
			t.Fatalf("brief missing %q:\n%s", fragment, brief.Text)
		}
	}
	if searchCalls.Load() != 1 || recallCalls.Load() != 1 || valueCalls.Load() != 0 {
		t.Fatalf("tool calls search=%d recall=%d value=%d",
			searchCalls.Load(), recallCalls.Load(), valueCalls.Load())
	}
	if len(sink.delivered) != 1 || sink.delivered[0].GeneratedAt != now {
		t.Fatalf("delivered briefs = %+v", sink.delivered)
	}
	next, err := morning.NextDelivery(now, profile)
	if err != nil || !next.Equal(time.Date(2026, 7, 19, 8, 15, 0, 0, time.UTC)) {
		t.Fatalf("next delivery = %s, %v", next, err)
	}
}

type fixedClock struct{ at time.Time }

func (clock fixedClock) Now() time.Time { return clock.at }

func TestAutomatrixAcceptanceCaptureAndThreeNonNegotiables(t *testing.T) {
	ctx := context.Background()
	queue := automatrix.NewQueue()
	now := time.Unix(5_000, 0)
	capturer, err := automatrix.NewCapturer(queue, fixedClock{at: now})
	if err != nil {
		t.Fatal(err)
	}
	item, err := capturer.Detect(
		ctx, "We should probably add monitoring to that service.", automatrix.DamageRisk{},
	)
	if err != nil || item == nil {
		t.Fatalf("capture = %+v, %v", item, err)
	}
	if item.Source != automatrix.SourceConversation ||
		item.Description != "add monitoring to that service" ||
		len(queue.Snapshot()) != 1 {
		t.Fatalf("captured item = %+v", item)
	}
	for _, risk := range []automatrix.DamageRisk{
		{Monetary: true}, {Reputational: true}, {Psychological: true},
	} {
		if _, err := capturer.Capture(ctx, automatrix.Opportunity{
			Description: "unsafe work", Risk: risk,
		}); !errors.Is(err, automatrix.ErrNonNegotiable) {
			t.Fatalf("risk %+v error = %v", risk, err)
		}
	}
	unsafe := *item
	unsafe.ID = [16]byte{1}
	unsafe.Risk = automatrix.DamageRisk{Monetary: true}
	if err := queue.Enqueue(ctx, unsafe); err == nil {
		t.Fatal("queue accepted manually injected damaging work")
	}
}

type soulCore struct {
	mu       sync.Mutex
	seenSoul []string
	sessions map[string][]string
}

func (core *soulCore) Respond(_ context.Context, turn gateway.Turn) (string, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.sessions == nil {
		core.sessions = make(map[string][]string)
	}
	core.seenSoul = append(core.seenSoul, turn.Soul)
	previous := append([]string(nil), core.sessions[turn.SessionKey]...)
	core.sessions[turn.SessionKey] = append(core.sessions[turn.SessionKey], turn.Inbound.Text)
	return fmt.Sprintf("soul=%s previous=%v", turn.Soul, previous), nil
}

type deliveryConnector struct{ platform gateway.Platform }

func (connector deliveryConnector) Platform() gateway.Platform         { return connector.platform }
func (deliveryConnector) Send(context.Context, gateway.Outbound) error { return nil }

func TestSOULAcceptanceLoadedEveryChannelInteractionAndIsolation(t *testing.T) {
	root := t.TempDir()
	path := root + "/SOUL.md"
	if err := os.WriteFile(path, []byte("# SOUL\nprecise"), 0o600); err != nil {
		t.Fatal(err)
	}
	soul, err := identity.NewFile(root)
	if err != nil {
		t.Fatal(err)
	}
	core := &soulCore{}
	channelGateway, err := gateway.New(
		core, []byte("0123456789abcdef0123456789abcdef"), soul,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range []gateway.Platform{gateway.Telegram, gateway.Discord} {
		if err := channelGateway.Register(deliveryConnector{platform: platform}); err != nil {
			t.Fatal(err)
		}
	}
	telegram := gateway.Inbound{
		Platform: gateway.Telegram, ConversationID: "chat", SenderID: "user",
		MessageID: "1", Text: "telegram secret",
	}
	first, err := channelGateway.Handle(context.Background(), telegram)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# SOUL\nprecise and candid"), 0o600); err != nil {
		t.Fatal(err)
	}
	discord := gateway.Inbound{
		Platform: gateway.Discord, ScopeID: "guild", ConversationID: "chat",
		SenderID: "user", MessageID: "2", Text: "discord secret",
	}
	second, err := channelGateway.Handle(context.Background(), discord)
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionKey == second.SessionKey ||
		!strings.Contains(second.Text, "precise and candid") ||
		strings.Contains(second.Text, "telegram secret") {
		t.Fatalf("second channel response = %q", second.Text)
	}
	if len(core.seenSoul) != 2 || core.seenSoul[0] == core.seenSoul[1] {
		t.Fatalf("SOUL loads = %v", core.seenSoul)
	}
}

func TestWeeklyIntegrityDigestAcceptanceRestartAndDurableDelivery(t *testing.T) {
	now := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	sourcePath := t.TempDir() + "/changes.jsonl"
	sinkPath := t.TempDir() + "/digests.jsonl"
	categories := []integrity.Category{
		integrity.CassandraEdit, integrity.EmotionalChange, integrity.TrustChange,
		integrity.DreamBelief, integrity.SelfModelUpdate,
	}
	recorder, err := integrity.NewJSONLSource(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	for index, category := range categories {
		if err := recorder.Record(context.Background(), integrity.Change{
			Category: category, At: now.Add(-time.Duration(index+1) * time.Hour),
			Summary: string(category), Evidence: json.RawMessage(`{"verified":true}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Constructing the source after the journal write simulates process restart.
	source, err := integrity.NewJSONLSource(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	sink, err := integrity.NewJournalSink(sinkPath)
	if err != nil {
		t.Fatal(err)
	}
	generator, err := integrity.New([]integrity.Source{source}, sink)
	if err != nil {
		t.Fatal(err)
	}
	report, err := generator.Run(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Changes) != 5 || len(report.Digest) != 64 {
		t.Fatalf("integrity report = %+v", report)
	}
	if !integrity.Verify(report) {
		t.Fatal("generated integrity digest did not verify")
	}
	for _, category := range categories {
		if report.Counts[category] != 1 {
			t.Fatalf("%s count = %d", category, report.Counts[category])
		}
	}
	delivered, err := os.ReadFile(sinkPath)
	if err != nil {
		t.Fatal(err)
	}
	var durable integrity.Report
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(delivered))), &durable); err != nil {
		t.Fatal(err)
	}
	if durable.Digest != report.Digest {
		t.Fatalf("durable digest = %s, want %s", durable.Digest, report.Digest)
	}
	durable.Changes[0].Summary = "tampered"
	if integrity.Verify(durable) {
		t.Fatal("tampered weekly digest verified")
	}
	next, err := integrity.NextWeekly(now, time.Monday, 8, 0, time.UTC)
	if err != nil || !next.After(now) || next.Sub(now) > 7*24*time.Hour {
		t.Fatalf("next weekly = %s, %v", next, err)
	}
}

func presenceManager(t *testing.T) *tools.Manager {
	t.Helper()
	layer := func(name policy.LayerName) policy.Layer {
		return policy.LayerFunc{
			LayerName: name,
			EvaluateFunc: func(context.Context, policy.Request) (policy.Result, error) {
				return policy.Result{Decision: policy.Allow}, nil
			},
		}
	}
	pipeline, err := policy.New(
		types.SystemClock{}, &policy.MemoryAuditor{},
		layer(policy.SandboxLayer), layer(policy.ProfileLayer),
		layer(policy.ProviderLayer), layer(policy.SenderLayer),
		layer(policy.GroupLayer),
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tools.NewManager(types.SystemClock{}, tools.WithExecutionPolicy(pipeline))
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
