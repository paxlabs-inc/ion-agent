// Package integrity produces and durably delivers the weekly safety digest.
package integrity

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lukechampine.com/blake3"
)

const Window = 7 * 24 * time.Hour

type Category string

const (
	CassandraEdit   Category = "cassandra_edit"
	EmotionalChange Category = "emotional_state_transition"
	TrustChange     Category = "trust_level_change"
	DreamBelief     Category = "dreamweaver_belief"
	SelfModelUpdate Category = "self_model_update"
)

func (category Category) Valid() bool {
	switch category {
	case CassandraEdit, EmotionalChange, TrustChange, DreamBelief, SelfModelUpdate:
		return true
	default:
		return false
	}
}

type Change struct {
	Category Category        `json:"category"`
	At       time.Time       `json:"at"`
	Summary  string          `json:"summary"`
	Evidence json.RawMessage `json:"evidence,omitempty"`
}

type Source interface {
	Changes(context.Context, time.Time, time.Time) ([]Change, error)
}

type Sink interface {
	Deliver(context.Context, Report) error
}

type Report struct {
	From      time.Time        `json:"from"`
	Until     time.Time        `json:"until"`
	Generated time.Time        `json:"generated"`
	Counts    map[Category]int `json:"counts"`
	Changes   []Change         `json:"changes"`
	Digest    string           `json:"digest"`
}

// Verify recomputes the report hash over the digest-free canonical payload.
func Verify(report Report) bool {
	expected, err := hex.DecodeString(report.Digest)
	if err != nil || len(expected) != 32 {
		return false
	}
	report.Digest = ""
	canonical, err := json.Marshal(report)
	if err != nil {
		return false
	}
	sum := blake3.Sum256(canonical)
	return subtle.ConstantTimeCompare(expected, sum[:]) == 1
}

type Generator struct {
	sources []Source
	sink    Sink
}

func New(sources []Source, sink Sink) (*Generator, error) {
	if len(sources) == 0 || sink == nil {
		return nil, fmt.Errorf("integrity: sources and sink are required")
	}
	for _, source := range sources {
		if source == nil {
			return nil, fmt.Errorf("integrity: nil source")
		}
	}
	return &Generator{sources: append([]Source(nil), sources...), sink: sink}, nil
}

// Run is the operational phase gate: the digest is not successful until its
// sink has durably accepted the report.
func (generator *Generator) Run(ctx context.Context, until time.Time) (Report, error) {
	if until.IsZero() {
		return Report{}, fmt.Errorf("integrity: report time is required")
	}
	report := Report{
		From: until.Add(-Window), Until: until, Generated: until,
		Counts: map[Category]int{
			CassandraEdit: 0, EmotionalChange: 0, TrustChange: 0,
			DreamBelief: 0, SelfModelUpdate: 0,
		},
	}
	for _, source := range generator.sources {
		changes, err := source.Changes(ctx, report.From, report.Until)
		if err != nil {
			return Report{}, fmt.Errorf("integrity: collect: %w", err)
		}
		for _, change := range changes {
			if err := validateChange(change, report.From, report.Until); err != nil {
				return Report{}, err
			}
			change.Evidence = append(json.RawMessage(nil), change.Evidence...)
			report.Changes = append(report.Changes, change)
			report.Counts[change.Category]++
		}
	}
	sort.Slice(report.Changes, func(i, j int) bool {
		if !report.Changes[i].At.Equal(report.Changes[j].At) {
			return report.Changes[i].At.Before(report.Changes[j].At)
		}
		if report.Changes[i].Category != report.Changes[j].Category {
			return report.Changes[i].Category < report.Changes[j].Category
		}
		return report.Changes[i].Summary < report.Changes[j].Summary
	})
	canonical, err := json.Marshal(report)
	if err != nil {
		return Report{}, err
	}
	sum := blake3.Sum256(canonical)
	report.Digest = hex.EncodeToString(sum[:])
	if err := generator.sink.Deliver(ctx, report); err != nil {
		return Report{}, fmt.Errorf("integrity: deliver: %w", err)
	}
	return cloneReport(report), nil
}

func NextWeekly(now time.Time, weekday time.Weekday, hour, minute int, location *time.Location) (time.Time, error) {
	if location == nil || weekday < time.Sunday || weekday > time.Saturday ||
		hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return time.Time{}, fmt.Errorf("integrity: valid weekly schedule is required")
	}
	local := now.In(location)
	days := (int(weekday) - int(local.Weekday()) + 7) % 7
	next := time.Date(local.Year(), local.Month(), local.Day(),
		hour, minute, 0, 0, location).AddDate(0, 0, days)
	if !next.After(local) {
		next = next.AddDate(0, 0, 7)
	}
	return next, nil
}

func validateChange(change Change, from, until time.Time) error {
	if !change.Category.Valid() || strings.TrimSpace(change.Summary) == "" ||
		change.At.Before(from) || change.At.After(until) ||
		(len(change.Evidence) > 0 && !json.Valid(change.Evidence)) {
		return fmt.Errorf("integrity: invalid or out-of-window change")
	}
	return nil
}

func cloneReport(report Report) Report {
	report.Counts = cloneCounts(report.Counts)
	report.Changes = append([]Change(nil), report.Changes...)
	for index := range report.Changes {
		report.Changes[index].Evidence = append(json.RawMessage(nil), report.Changes[index].Evidence...)
	}
	return report
}

func cloneCounts(counts map[Category]int) map[Category]int {
	copy := make(map[Category]int, len(counts))
	for category, count := range counts {
		copy[category] = count
	}
	return copy
}

// JSONLSource reads append-only, restart-safe change evidence.
type JSONLSource struct {
	path string
	mu   sync.Mutex
}

// Record is the common append-only hook used by Cassandra, emotional state,
// relationship, Dreamweaver, and self-model owners.
func (source *JSONLSource) Record(ctx context.Context, change Change) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !change.Category.Valid() || change.At.IsZero() ||
		strings.TrimSpace(change.Summary) == "" ||
		(len(change.Evidence) > 0 && !json.Valid(change.Evidence)) {
		return fmt.Errorf("integrity: invalid change")
	}
	payload, err := json.Marshal(change)
	if err != nil {
		return err
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	file, err := os.OpenFile(source.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := os.Chmod(source.path, 0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func NewJSONLSource(path string) (*JSONLSource, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("integrity: source path is required")
	}
	return &JSONLSource{path: absolute}, nil
}

func (source *JSONLSource) Changes(
	ctx context.Context,
	from time.Time,
	until time.Time,
) ([]Change, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	file, err := os.Open(source.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var changes []Change
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var change Change
		if err := json.Unmarshal(scanner.Bytes(), &change); err != nil {
			return nil, fmt.Errorf("integrity: decode source: %w", err)
		}
		if !change.At.Before(from) && !change.At.After(until) {
			changes = append(changes, change)
		}
	}
	return changes, scanner.Err()
}

// JournalSink appends delivered reports and fsyncs before success.
type JournalSink struct {
	path string
	mu   sync.Mutex
}

func NewJournalSink(path string) (*JournalSink, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("integrity: sink path is required")
	}
	return &JournalSink{path: absolute}, nil
}

func (sink *JournalSink) Deliver(ctx context.Context, report Report) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	file, err := os.OpenFile(sink.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := os.Chmod(sink.path, 0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
