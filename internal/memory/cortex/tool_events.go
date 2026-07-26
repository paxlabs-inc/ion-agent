package cortex

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/mmr"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// CommitToolEvent durably stores an execution event and returns its MMR receipt.
// Receipt fields are deliberately populated after encryption and hashing, so
// they are derived metadata rather than self-referential journal content.
func (c *Cortex) CommitToolEvent(
	ctx context.Context,
	event protocol.ToolEvent,
) (*protocol.ToolEvent, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	event.MMRLeafIndex = 0
	event.MMRLeafHash = [32]byte{}
	event.MMRRootAtTime = [32]byte{}
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	_, receipt, err := c.writeWithIDAndCommitment(
		ctx,
		event.ID,
		c.actor,
		memory.Event,
		encoded,
		"agent-loop",
		TrustVerified,
	)
	if err != nil {
		return nil, err
	}
	event.MMRLeafIndex = receipt.MMRLeafIndex
	copy(event.MMRLeafHash[:], receipt.MMRLeafHash[:])
	copy(event.MMRRootAtTime[:], receipt.MMRRoot[:])
	return &event, nil
}

// GetToolEvent retrieves a committed event and reconstructs its derived receipt.
func (c *Cortex) GetToolEvent(id uuid.UUID) (*protocol.ToolEvent, bool) {
	resolved, err := c.Resolve(id)
	if err != nil || resolved.Head.Type != memory.Event {
		return nil, false
	}
	var event protocol.ToolEvent
	if err := json.Unmarshal(resolved.Version.Data, &event); err != nil {
		return nil, false
	}
	if err := event.Validate(); err != nil {
		return nil, false
	}
	c.mu.RLock()
	receipt, ok := c.receipts[id]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	event.MMRLeafIndex = receipt.MMRLeafIndex
	copy(event.MMRLeafHash[:], receipt.MMRLeafHash[:])
	copy(event.MMRRootAtTime[:], receipt.MMRRoot[:])
	return &event, true
}

// VerifyCitation checks identity, historical leaf inclusion, and snapshot root.
func (c *Cortex) VerifyCitation(
	ctx context.Context,
	citation protocol.Citation,
	event protocol.ToolEvent,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := citation.Validate(); err != nil {
		return false, err
	}
	committed, ok := c.GetToolEvent(citation.ToolEventID)
	if !ok || committed.ID != event.ID {
		return false, nil
	}
	if committed.MMRLeafHash != citation.MMRLeafHash ||
		committed.MMRRootAtTime != citation.MMRRootAtTime ||
		event.MMRLeafHash != committed.MMRLeafHash ||
		event.MMRRootAtTime != committed.MMRRootAtTime {
		return false, nil
	}
	leaf := mmr.Hash(committed.MMRLeafHash)
	root := mmr.Hash(committed.MMRRootAtTime)
	proof, err := c.mmrState.ProveAt(
		committed.MMRLeafIndex,
		committed.MMRLeafIndex+1,
	)
	if err != nil {
		return false, err
	}
	return mmr.VerifyProof(leaf, proof, root), nil
}

// RecordPrediction durably journals a prediction/outcome comparison.
func (c *Cortex) RecordPrediction(
	ctx context.Context,
	record protocol.PredictionRecord,
) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = c.Write(ctx, memory.Event, encoded, "prediction-engine")
	return err
}
