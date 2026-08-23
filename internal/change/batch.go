package change

import (
	"context"
	"errors"
	"sync"
)

// Batch holds the exclusive per-Vault operation lock across recovery,
// whole-batch preflight, and every sequential mutation.
type Batch interface {
	Recover(context.Context) (RecoveryOutcome, error)
	Validate(context.Context, PreparedChange) error
	Apply(context.Context, PreparedChange) (Outcome, error)
	ApplyLive(context.Context, PreparedChange, RuntimeSession) (Outcome, error)
	Close() error
}

type vaultBatch struct {
	engine *Engine
	paths  vaultPaths
	lock   *vaultLock

	mu     sync.Mutex
	closed bool
}

// BeginBatch acquires the Vault operation lock. Callers must Close the returned
// Batch and must perform recovery and all mutation preflight through it.
func (e *Engine) BeginBatch(ctx context.Context, vaultRoot string) (Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths, err := vaultPathsFor(vaultRoot, "")
	if err != nil {
		return nil, err
	}
	lock, err := acquireVaultLock(paths.lock)
	if err != nil {
		return nil, err
	}
	return &vaultBatch{engine: e, paths: paths, lock: lock}, nil
}

func (b *vaultBatch) Recover(ctx context.Context) (RecoveryOutcome, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return RecoveryOutcome{}, errors.New("change Batch is closed")
	}
	if err := ctx.Err(); err != nil {
		return RecoveryOutcome{}, err
	}
	return recoverLocked(b.paths)
}

func (b *vaultBatch) Validate(ctx context.Context, change PreparedChange) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("change Batch is closed")
	}
	return b.engine.validateInBatch(ctx, change, b.paths.root)
}

func (b *vaultBatch) Apply(ctx context.Context, change PreparedChange) (Outcome, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return Outcome{PluginID: change.PluginID}, errors.New("change Batch is closed")
	}
	return b.engine.applyLocked(ctx, change, nil, b.paths.root)
}

func (b *vaultBatch) ApplyLive(ctx context.Context, change PreparedChange, runtime RuntimeSession) (Outcome, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return Outcome{PluginID: change.PluginID}, errors.New("change Batch is closed")
	}
	if runtime == nil {
		return Outcome{PluginID: change.PluginID}, errors.New("runtime session is required")
	}
	return b.engine.applyLocked(ctx, change, runtime, b.paths.root)
}

func (b *vaultBatch) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	b.lock.release()
	return nil
}
