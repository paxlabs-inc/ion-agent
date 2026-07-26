package automatrix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

var ErrNonNegotiable = errors.New("automatrix: opportunity violates non-negotiables")

// Opportunity is a reviewed implied opportunity before it enters idle work.
type Opportunity struct {
	Description string
	Priority    float64
	Risk        DamageRisk
}

// Capturer converts implied conversational language into durable-ready work.
type Capturer struct {
	queue *Queue
	clock types.Clock
}

func NewCapturer(queue *Queue, clock types.Clock) (*Capturer, error) {
	if queue == nil || clock == nil {
		return nil, fmt.Errorf("automatrix: queue and clock are required")
	}
	return &Capturer{queue: queue, clock: clock}, nil
}

// Detect recognizes non-imperative opportunity phrases. Direct commands are
// handled by the normal agent path rather than silently becoming idle work.
func (capturer *Capturer) Detect(
	ctx context.Context,
	conversation string,
	risk DamageRisk,
) (*WorkItem, error) {
	text := strings.TrimSpace(conversation)
	lower := strings.ToLower(text)
	var marker string
	for _, candidate := range []string{
		"we should probably ", "we should consider ", "it would be useful to ",
		"someday we could ", "we might want to ",
	} {
		if index := strings.Index(lower, candidate); index >= 0 {
			marker = strings.TrimSpace(text[index+len(candidate):])
			break
		}
	}
	if marker == "" {
		return nil, nil
	}
	marker = strings.TrimSpace(strings.TrimRight(marker, ".!?"))
	if marker == "" {
		return nil, nil
	}
	return capturer.Capture(ctx, Opportunity{
		Description: marker, Priority: 0.5, Risk: risk,
	})
}

func (capturer *Capturer) Capture(
	ctx context.Context,
	opportunity Opportunity,
) (*WorkItem, error) {
	if opportunity.Risk.Any() {
		return nil, ErrNonNegotiable
	}
	description := strings.TrimSpace(opportunity.Description)
	if description == "" || opportunity.Priority < 0 {
		return nil, fmt.Errorf("automatrix: valid opportunity is required")
	}
	payload, _ := json.Marshal(map[string]string{
		"origin": "implied_conversation_opportunity",
		"text":   description,
	})
	id := uuid.New()
	arguments, _ := json.Marshal(map[string]any{
		"query": description, "limit": 20,
	})
	item := WorkItem{
		ID: id, Source: SourceConversation, Kind: "implied_opportunity",
		Description: description, Priority: opportunity.Priority,
		Payload: payload, Risk: opportunity.Risk, CreatedAt: capturer.clock.Now(),
		Actions: []Action{{ToolCall: &protocol.NormalizedToolCall{
			ID: "automatrix-" + id.String(), Name: "memory_search",
			Arguments: arguments,
		}}},
	}
	if err := capturer.queue.Enqueue(ctx, item); err != nil {
		return nil, err
	}
	copy := cloneItem(item)
	return &copy, nil
}
