// Package obsidian coordinates plugin replacement with a running Obsidian
// instance. It never performs filesystem replacement itself.
package obsidian

import (
	"context"
	"errors"
	"fmt"
)

// Client is the narrow runtime surface the coordinator requires. Production
// adapters may use an Obsidian CLI or another supported runtime bridge.
type Client interface {
	Probe(context.Context) error
	Inspect(context.Context, string) (PluginState, error)
	Disable(context.Context, string) error
	Unload(context.Context, string) error
	Refresh(context.Context) error
	Reload(context.Context, string) error
	Enable(context.Context, string) error
}

// PluginState is the part of plugin state controlled or observed at runtime.
type PluginState struct {
	Present bool
	ID      string
	Version string
	Enabled bool
	Loaded  bool
}

// IsQuiescedCache reports whether Obsidian is only remembering a manifest for
// a plugin whose files are already gone: present but neither loaded nor
// enabled. Live coordination treats this phantom state as absent.
func (s PluginState) IsQuiescedCache() bool {
	return s.Present && !s.Loaded && !s.Enabled
}

// ChangePlan binds an already-planned filesystem change to live Obsidian
// state. The plan contains no filesystem callbacks; change.Engine owns the
// Restore Point and the mutation lifecycle.
type ChangePlan struct {
	PluginID      string
	PlannedState  PluginState
	TargetVersion string
	EnableNew     bool
	// TargetAbsent selects uninstall semantics. Commit verifies that the
	// quiesced plugin is absent and never asks Obsidian to reload removed files.
	TargetAbsent bool
}

// LiveSession implements change.RuntimeSession without making the filesystem
// engine depend on Obsidian. Create one with Coordinator.Session for each
// individual plugin change.
type LiveSession struct {
	client            Client
	plan              ChangePlan
	prior             PluginState
	hasPrior          bool
	quiesceAttempted  bool
	activationStarted bool
}

// ReplaceRequest describes one already-planned plugin transition.
type ReplaceRequest struct {
	PluginID      string
	PlannedState  PluginState
	TargetVersion string
	EnableNew     bool
	ReplaceFiles  func(context.Context) error
	RestoreFiles  func(context.Context) error
}

// RestoreOutcome reports the best-effort recovery performed after mutation.
type RestoreOutcome struct {
	Attempted       bool
	FilesRestored   bool
	RuntimeRestored bool
	Err             error
}

// ReplaceResult reports the verified resulting state or recovery outcome.
type ReplaceResult struct {
	State   PluginState
	Restore RestoreOutcome
}

// Coordinator makes a staged replacement safe for a live Obsidian runtime.
type Coordinator struct {
	client Client
}

func NewCoordinator(client Client) *Coordinator {
	return &Coordinator{client: client}
}

// Session binds a live runtime plan for use with change.Engine.ApplyLive.
func (c *Coordinator) Session(plan ChangePlan) *LiveSession {
	return &LiveSession{client: c.client, plan: plan}
}

// Prepare probes and rechecks Obsidian immediately before filesystem mutation,
// then unloads the prior plugin when necessary.
func (s *LiveSession) Prepare(ctx context.Context) error {
	if s.client == nil {
		return errors.New("obsidian runtime client is required")
	}
	if s.plan.PluginID == "" || (!s.plan.TargetAbsent && s.plan.TargetVersion == "") {
		return errors.New("incomplete live change plan")
	}
	if s.plan.TargetAbsent && !s.plan.PlannedState.Present {
		return errors.New("live uninstall requires a present planned plugin state")
	}
	if err := s.client.Probe(ctx); err != nil {
		return fmt.Errorf("probe Obsidian runtime: %w", err)
	}
	snapshot, err := s.client.Inspect(ctx, s.plan.PluginID)
	if err != nil {
		return fmt.Errorf("inspect plugin runtime state: %w", err)
	}
	if snapshot != s.plan.PlannedState {
		return fmt.Errorf("plugin state changed before live replacement: %w", ErrStateChanged)
	}
	s.prior = snapshot
	s.hasPrior = true
	rechecked, err := s.client.Inspect(ctx, s.plan.PluginID)
	if err != nil {
		return fmt.Errorf("recheck plugin runtime state: %w", err)
	}
	if rechecked != snapshot {
		return fmt.Errorf("plugin state changed during live replacement: %w", ErrStateChanged)
	}
	if snapshot.Enabled {
		s.quiesceAttempted = true
		if err := s.client.Disable(ctx, s.plan.PluginID); err != nil {
			return fmt.Errorf("disable plugin: %w", err)
		}
	} else if snapshot.Loaded {
		s.quiesceAttempted = true
		if err := s.client.Unload(ctx, s.plan.PluginID); err != nil {
			return fmt.Errorf("unload plugin: %w", err)
		}
	}
	return nil
}

// Commit reloads the replacement, restores the intended enabled state, and
// verifies the live result before Engine may remove its Restore Point.
func (s *LiveSession) Commit(ctx context.Context) error {
	if !s.hasPrior {
		return errors.New("live session was not prepared")
	}
	s.activationStarted = true
	if s.plan.TargetAbsent {
		state, err := s.client.Inspect(ctx, s.plan.PluginID)
		if err != nil {
			return fmt.Errorf("verify removed plugin runtime state: %w", err)
		}
		if state != (PluginState{}) && !(state.Present && state.ID == s.plan.PluginID && !state.Enabled && !state.Loaded) {
			return fmt.Errorf("verify removed plugin runtime state: got %+v, want absent or quiesced cached manifest", state)
		}
		return nil
	}
	if err := s.client.Refresh(ctx); err != nil {
		return fmt.Errorf("refresh plugin manifests: %w", err)
	}
	if s.prior.Enabled {
		if err := s.client.Reload(ctx, s.plan.PluginID); err != nil {
			return fmt.Errorf("reload plugin: %w", err)
		}
	}
	wantEnabled := s.prior.Enabled || (!s.prior.Present && s.plan.EnableNew)
	if wantEnabled {
		if err := s.client.Enable(ctx, s.plan.PluginID); err != nil {
			return fmt.Errorf("restore plugin enabled state: %w", err)
		}
	}
	state, err := s.client.Inspect(ctx, s.plan.PluginID)
	if err != nil {
		return fmt.Errorf("verify plugin runtime state: %w", err)
	}
	want := PluginState{Present: true, ID: s.plan.PluginID, Version: s.plan.TargetVersion, Enabled: wantEnabled, Loaded: wantEnabled}
	if state != want {
		return fmt.Errorf("verify plugin runtime state: got %+v, want %+v", state, want)
	}
	return nil
}

// Rollback restores and verifies the prior runtime state. Engine invokes it
// only after prior files and enabled configuration have been restored.
func (s *LiveSession) Rollback(ctx context.Context) error {
	if !s.hasPrior || (!s.quiesceAttempted && !s.activationStarted) {
		return nil
	}
	if s.activationStarted {
		if err := s.client.Refresh(ctx); err != nil {
			return fmt.Errorf("refresh restored plugin manifests: %w", err)
		}
	}
	if s.prior.Loaded {
		if err := s.client.Reload(ctx, s.plan.PluginID); err != nil {
			return fmt.Errorf("reload restored plugin: %w", err)
		}
		if s.prior.Enabled {
			if err := s.client.Enable(ctx, s.plan.PluginID); err != nil {
				return fmt.Errorf("re-enable restored plugin: %w", err)
			}
		}
	}
	state, err := s.client.Inspect(ctx, s.plan.PluginID)
	if err != nil {
		return fmt.Errorf("verify restored plugin: %w", err)
	}
	if state != s.prior {
		return fmt.Errorf("verify restored plugin: got %+v, want %+v", state, s.prior)
	}
	return nil
}

// Replace quiesces a plugin, invokes the supplied filesystem replacement,
// reloads it, restores its intended enabled state, and verifies the result.
func (c *Coordinator) Replace(ctx context.Context, req ReplaceRequest) (ReplaceResult, error) {
	if c.client == nil {
		return ReplaceResult{}, errors.New("obsidian runtime client is required")
	}
	if req.PluginID == "" || req.TargetVersion == "" || req.ReplaceFiles == nil || req.RestoreFiles == nil {
		return ReplaceResult{}, errors.New("incomplete live replacement request")
	}
	session := c.Session(ChangePlan{
		PluginID: req.PluginID, PlannedState: req.PlannedState,
		TargetVersion: req.TargetVersion, EnableNew: req.EnableNew,
	})
	if err := session.Prepare(ctx); err != nil {
		return ReplaceResult{}, err
	}

	if err := req.ReplaceFiles(ctx); err != nil {
		result := restoreCallbacks(ctx, req, session)
		return result, fmt.Errorf("replace plugin files: %w", err)
	}
	if err := session.Commit(ctx); err != nil {
		result := restoreCallbacks(ctx, req, session)
		return result, err
	}
	wantEnabled := req.PlannedState.Enabled || (!req.PlannedState.Present && req.EnableNew)
	return ReplaceResult{State: PluginState{
		Present: true, ID: req.PluginID, Version: req.TargetVersion,
		Enabled: wantEnabled, Loaded: wantEnabled,
	}}, nil
}

func restoreCallbacks(ctx context.Context, req ReplaceRequest, session *LiveSession) ReplaceResult {
	outcome := RestoreOutcome{Attempted: true}
	if err := req.RestoreFiles(ctx); err != nil {
		outcome.Err = fmt.Errorf("restore plugin files: %w", err)
		return ReplaceResult{Restore: outcome}
	}
	outcome.FilesRestored = true
	if err := session.Rollback(ctx); err != nil {
		outcome.Err = err
		return ReplaceResult{Restore: outcome}
	}
	outcome.RuntimeRestored = true
	return ReplaceResult{State: session.prior, Restore: outcome}
}

// ErrStateChanged means Obsidian state no longer matches the applied plan.
var ErrStateChanged = errors.New("concurrent plugin state change")
