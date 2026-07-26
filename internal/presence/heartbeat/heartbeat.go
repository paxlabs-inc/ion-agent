// Package heartbeat implements the non-blocking 60-second presence pulse.
package heartbeat

import (
	"context"
	"fmt"
	"time"
)

const Interval = 60 * time.Second

type Check string

const (
	CheckCron        Check = "cron"
	CheckAutomatrix  Check = "automatrix"
	CheckSubagents   Check = "subagents"
	CheckEmotional   Check = "emotional_state"
	CheckDreamweaver Check = "dreamweaver"
)

// Signals are cap-1 worker queues. Pulse only attempts non-blocking sends.
type Signals struct {
	Cron        chan<- struct{}
	Automatrix  chan<- struct{}
	Subagents   chan<- struct{}
	Emotional   chan<- struct{}
	Dreamweaver chan<- struct{}
}

// Beat records which subsystem signals were accepted. A false value means the
// previous check is still queued; the heartbeat never waits for it.
type Beat struct {
	At       time.Time      `json:"at"`
	Accepted map[Check]bool `json:"accepted"`
}

type Heartbeat struct {
	signals Signals
	idle    func() bool
	report  chan<- Beat
}

func New(signals Signals, idle func() bool, report chan<- Beat) (*Heartbeat, error) {
	if signals.Cron == nil || signals.Automatrix == nil ||
		signals.Subagents == nil || signals.Emotional == nil ||
		signals.Dreamweaver == nil || idle == nil {
		return nil, fmt.Errorf("heartbeat: five signals and idle check are required")
	}
	return &Heartbeat{signals: signals, idle: idle, report: report}, nil
}

// Pulse contains no blocking operation. The idle callback must be an atomic or
// lock-free state read supplied by the owner.
func (heartbeat *Heartbeat) Pulse(at time.Time) Beat {
	beat := Beat{At: at, Accepted: make(map[Check]bool, 5)}
	beat.Accepted[CheckCron] = offer(heartbeat.signals.Cron)
	beat.Accepted[CheckAutomatrix] = offer(heartbeat.signals.Automatrix)
	beat.Accepted[CheckSubagents] = offer(heartbeat.signals.Subagents)
	beat.Accepted[CheckEmotional] = offer(heartbeat.signals.Emotional)
	if heartbeat.idle() {
		beat.Accepted[CheckDreamweaver] = offer(heartbeat.signals.Dreamweaver)
	} else {
		beat.Accepted[CheckDreamweaver] = false
	}
	if heartbeat.report != nil {
		select {
		case heartbeat.report <- beat:
		default:
		}
	}
	return beat
}

func offer(channel chan<- struct{}) bool {
	select {
	case channel <- struct{}{}:
		return true
	default:
		return false
	}
}

// Run is the production 60-second loop. The caller owns cancellation.
func (heartbeat *Heartbeat) Run(ctx context.Context) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case at := <-ticker.C:
			heartbeat.Pulse(at)
		}
	}
}

// RunTicks provides the same loop over an injected ticker for deterministic
// acceptance testing of exact cadence.
func (heartbeat *Heartbeat) RunTicks(ctx context.Context, ticks <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case at, open := <-ticks:
			if !open {
				return
			}
			heartbeat.Pulse(at)
		}
	}
}
