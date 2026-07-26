// Package morning implements the profile-driven, read-only morning brief.
package morning

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

var allowedTools = map[string]struct{}{
	"web_search":    {},
	"memory_recall": {},
}

type Kind string

const (
	PRMerged        Kind = "pr_merged"
	IssueOpened     Kind = "issue_opened"
	DeploySucceeded Kind = "deploy_succeeded"
	CalendarEvent   Kind = "calendar_event"
)

type Item struct {
	Kind   Kind      `json:"kind"`
	Title  string    `json:"title"`
	URL    string    `json:"url,omitempty"`
	At     time.Time `json:"at"`
	Source string    `json:"source"`
}

// Source is a true external-boundary adapter (GitHub, deployment system, or
// calendar). It returns typed, timestamped data rather than model prose.
type Source interface {
	Collect(context.Context, time.Time, time.Time) ([]Item, error)
}

type ToolExecutor interface {
	Execute(context.Context, protocol.NormalizedToolCall) (json.RawMessage, error)
}

type Sink interface {
	Deliver(context.Context, Brief) error
}

type Profile struct {
	Name          string
	Hour          int
	Minute        int
	Location      *time.Location
	SearchQueries []string
	RecallQueries []string
}

type Brief struct {
	GeneratedAt time.Time                  `json:"generated_at"`
	Profile     string                     `json:"profile"`
	Items       map[Kind][]Item            `json:"items"`
	Context     map[string]json.RawMessage `json:"context,omitempty"`
	Text        string                     `json:"text"`
}

type Generator struct {
	sources []Source
	tools   ToolExecutor
}

// Service binds generation to delivery. A scheduled run is successful only
// after the configured sink accepts the brief.
type Service struct {
	generator *Generator
	sink      Sink
}

func NewService(generator *Generator, sink Sink) (*Service, error) {
	if generator == nil || sink == nil {
		return nil, fmt.Errorf("morning: generator and delivery sink are required")
	}
	return &Service{generator: generator, sink: sink}, nil
}

func (service *Service) RunOnce(
	ctx context.Context,
	profile Profile,
	since time.Time,
	now time.Time,
) (Brief, error) {
	brief, err := service.generator.Generate(ctx, profile, since, now)
	if err != nil {
		return Brief{}, err
	}
	if err := service.sink.Deliver(ctx, brief); err != nil {
		return Brief{}, fmt.Errorf("morning: deliver: %w", err)
	}
	return brief, nil
}

func NewGenerator(sources []Source, tools ToolExecutor) (*Generator, error) {
	if len(sources) == 0 || tools == nil {
		return nil, fmt.Errorf("morning: sources and tool executor are required")
	}
	for _, source := range sources {
		if source == nil {
			return nil, fmt.Errorf("morning: nil data source")
		}
	}
	return &Generator{sources: append([]Source(nil), sources...), tools: tools}, nil
}

func (generator *Generator) Generate(
	ctx context.Context,
	profile Profile,
	since time.Time,
	now time.Time,
) (Brief, error) {
	if err := validateProfile(profile); err != nil {
		return Brief{}, err
	}
	if since.IsZero() || now.IsZero() || !since.Before(now) {
		return Brief{}, fmt.Errorf("morning: valid collection window is required")
	}
	brief := Brief{
		GeneratedAt: now, Profile: profile.Name,
		Items: make(map[Kind][]Item), Context: make(map[string]json.RawMessage),
	}
	for _, source := range generator.sources {
		items, err := source.Collect(ctx, since, now)
		if err != nil {
			return Brief{}, fmt.Errorf("morning: collect: %w", err)
		}
		for _, item := range items {
			if err := validateItem(item, since, now); err != nil {
				return Brief{}, err
			}
			brief.Items[item.Kind] = append(brief.Items[item.Kind], item)
		}
	}
	for kind := range brief.Items {
		sort.Slice(brief.Items[kind], func(i, j int) bool {
			return brief.Items[kind][i].At.Before(brief.Items[kind][j].At)
		})
	}
	for index, query := range profile.SearchQueries {
		if err := generator.readOnlyCall(ctx, &brief, "web_search", index, query); err != nil {
			return Brief{}, err
		}
	}
	for index, query := range profile.RecallQueries {
		if err := generator.readOnlyCall(ctx, &brief, "memory_recall", index, query); err != nil {
			return Brief{}, err
		}
	}
	brief.Text = render(brief)
	return brief, nil
}

func (generator *Generator) readOnlyCall(
	ctx context.Context,
	brief *Brief,
	name string,
	index int,
	query string,
) error {
	if _, allowed := allowedTools[name]; !allowed {
		return fmt.Errorf("morning: tool %q is outside positive allowlist", name)
	}
	arguments, _ := json.Marshal(map[string]string{"query": query})
	result, err := generator.tools.Execute(ctx, protocol.NormalizedToolCall{
		ID:   fmt.Sprintf("morning-%s-%d", name, index),
		Name: name, Arguments: arguments,
	})
	if err != nil {
		return fmt.Errorf("morning: %s: %w", name, err)
	}
	brief.Context[fmt.Sprintf("%s.%d", name, index)] = append(json.RawMessage(nil), result...)
	return nil
}

func NextDelivery(now time.Time, profile Profile) (time.Time, error) {
	if err := validateProfile(profile); err != nil {
		return time.Time{}, err
	}
	local := now.In(profile.Location)
	next := time.Date(local.Year(), local.Month(), local.Day(),
		profile.Hour, profile.Minute, 0, 0, profile.Location)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next, nil
}

func validateProfile(profile Profile) error {
	if strings.TrimSpace(profile.Name) == "" || profile.Hour < 0 ||
		profile.Hour > 23 || profile.Minute < 0 || profile.Minute > 59 ||
		profile.Location == nil {
		return fmt.Errorf("morning: valid named profile, time, and location are required")
	}
	return nil
}

func validateItem(item Item, since, until time.Time) error {
	switch item.Kind {
	case PRMerged, IssueOpened, DeploySucceeded, CalendarEvent:
	default:
		return fmt.Errorf("morning: invalid item kind %q", item.Kind)
	}
	if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Source) == "" ||
		item.At.Before(since) || item.At.After(until) {
		return fmt.Errorf("morning: invalid or out-of-window item")
	}
	return nil
}

func render(brief Brief) string {
	var builder strings.Builder
	builder.WriteString("Good morning. Since the last brief:\n")
	for _, section := range []struct {
		kind  Kind
		label string
	}{
		{PRMerged, "PRs merged"}, {IssueOpened, "Issues opened"},
		{DeploySucceeded, "Deploys succeeded"}, {CalendarEvent, "Calendar events"},
	} {
		items := brief.Items[section.kind]
		fmt.Fprintf(&builder, "\n%s (%d):\n", section.label, len(items))
		for _, item := range items {
			fmt.Fprintf(&builder, "- %s [%s]\n", item.Title, item.Source)
		}
	}
	return strings.TrimSpace(builder.String())
}
