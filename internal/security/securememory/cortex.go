// Package securememory composes Cortex with mandatory honeypot checks so
// canary protection cannot be skipped by normal memory operations.
package securememory

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/security/canary"
)

// Cortex is the guarded memory boundary used by security-sensitive callers.
type Cortex struct {
	store    *cortex.Cortex
	canaries *canary.Manager
}

func New(store *cortex.Cortex, canaries *canary.Manager) (*Cortex, error) {
	if store == nil || canaries == nil {
		return nil, fmt.Errorf("securememory: Cortex and canary manager required")
	}
	return &Cortex{store: store, canaries: canaries}, nil
}

func (guard *Cortex) Write(
	ctx context.Context,
	memoryType memory.Type,
	data []byte,
	createdBy string,
) (*cortex.Memory, error) {
	return guard.store.Write(ctx, memoryType, data, createdBy)
}

// SeedCanary writes a real encrypted-journal Cortex memory and registers its
// generated ID as a honeypot before returning it to the caller.
func (guard *Cortex) SeedCanary(
	ctx context.Context,
	memoryType memory.Type,
	data []byte,
	createdBy string,
) (*cortex.Memory, error) {
	if err := memoryType.Validate(); err != nil {
		return nil, err
	}
	stored, err := guard.store.Write(ctx, memoryType, data, createdBy)
	if err != nil {
		return nil, err
	}
	if _, err := guard.canaries.Register(
		stored.Head.ID.String(),
		string(memoryType),
		string(data),
	); err != nil {
		return nil, fmt.Errorf("securememory: protect canary: %w", err)
	}
	return stored, nil
}

func (guard *Cortex) Resolve(id uuid.UUID, source string) (*cortex.Memory, error) {
	if guard.canaries.TrapAccess(id.String(), source) {
		return nil, fmt.Errorf("securememory: canary access blocked: %s", id)
	}
	return guard.store.Resolve(id)
}

func (guard *Cortex) Update(
	ctx context.Context,
	id uuid.UUID,
	data []byte,
	createdBy string,
) (*cortex.Memory, error) {
	if err := guard.canaries.ProtectMutation(id.String(), createdBy); err != nil {
		return nil, err
	}
	return guard.store.Update(ctx, id, data, createdBy)
}

func (guard *Cortex) Archive(
	ctx context.Context,
	id uuid.UUID,
	reason string,
	by string,
) error {
	if err := guard.canaries.ProtectArchive(id.String(), by); err != nil {
		return err
	}
	return guard.store.Tombstone(ctx, id, reason, by)
}
