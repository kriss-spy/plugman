// Package change applies one prepared plugin state transition at a time.
package change

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kriss-spy/plugman/internal/provenance"
)

// Kind identifies the filesystem transition to apply.
type Kind string

const (
	Install   Kind = "install"
	Update    Kind = "update"
	Uninstall Kind = "uninstall"
)

// EnabledDirective controls the final membership of community-plugins.json.
type EnabledDirective uint8

const (
	PreserveEnabled EnabledDirective = iota
	Enable
	Disable
)

// PreparedChange is a fully downloaded and resolved single-plugin change.
// StagedDir is required for installs and updates and is treated as read-only.
type PreparedChange struct {
	VaultRoot string
	PluginID  string
	Kind      Kind
	StagedDir string
	// SourceRecord is persisted for explicit GitHub installations and updates.
	SourceRecord *SourceRecord
	Enabled      EnabledDirective
	KeepData     bool
}

// SourceRecord is GitHub provenance persisted inside the installed plugin.
type SourceRecord struct {
	Repository string `json:"repository"`
	Release    string `json:"release"`
}

// Outcome describes whether the requested transition took effect or was restored.
type Outcome struct {
	PluginID string
	Changed  bool
	Restored bool
}

// RecoveryOutcome describes a prior interrupted transition handled by Recover.
type RecoveryOutcome struct {
	Recovered bool
	PluginID  string
}

// RecoveryRequiredError leaves recovery material in Path for manual handling.
type RecoveryRequiredError struct {
	Path string
	Err  error
}

func (e *RecoveryRequiredError) Error() string {
	return fmt.Sprintf("plugin recovery requires manual action at %s: %v", e.Path, e.Err)
}

func (e *RecoveryRequiredError) Unwrap() error { return e.Err }

// Engine applies closed-Vault changes. A zero Engine is ready for use.
type Engine struct {
	afterReplacement func() error
}

func New() *Engine { return &Engine{} }

// RuntimeSession coordinates a live consumer around one filesystem mutation.
// Prepare must quiesce and revalidate the consumer. Commit must reload and
// verify the new state. Rollback runs only after the Engine has restored the
// prior files and must restore and verify the prior runtime state.
type RuntimeSession interface {
	Prepare(context.Context) error
	Commit(context.Context) error
	Rollback(context.Context) error
}

// Validate checks one prepared transition without creating recovery or other
// Vault artifacts. Callers can validate a complete batch before the first Apply.
func (e *Engine) Validate(ctx context.Context, change PreparedChange) error {
	return e.validateInBatch(ctx, change, "")
}

func (e *Engine) validateInBatch(ctx context.Context, change PreparedChange, batchRoot string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	paths, _, err := validatePrepared(change)
	if err != nil {
		return err
	}
	if batchRoot != "" && paths.root != batchRoot {
		return errors.New("prepared change targets a different Vault than the active Batch")
	}
	return validateCurrentState(change, paths)
}

// Apply acquires the Vault lock, restores any interrupted change, then atomically
// replaces exactly one plugin. Once mutation starts, cancellation is deferred
// until the plugin is either verified or restored.
func (e *Engine) Apply(ctx context.Context, change PreparedChange) (Outcome, error) {
	return e.applyWithBatch(ctx, change, nil)
}

// ApplyLive keeps the Restore Point until the supplied runtime session has
// verified the replacement. Any failure after Prepare begins restores the
// prior files before asking the session to restore the prior runtime state.
func (e *Engine) ApplyLive(ctx context.Context, change PreparedChange, runtime RuntimeSession) (Outcome, error) {
	if runtime == nil {
		return Outcome{PluginID: change.PluginID}, errors.New("runtime session is required")
	}
	return e.applyWithBatch(ctx, change, runtime)
}

func (e *Engine) applyWithBatch(ctx context.Context, change PreparedChange, runtime RuntimeSession) (Outcome, error) {
	// Reject malformed or unsafe prepared input before creating even the
	// Vault-local operation lock. Batch.Apply repeats this validation while the
	// lock is held so current state cannot race the actual mutation.
	if _, _, err := validatePrepared(change); err != nil {
		return Outcome{PluginID: change.PluginID}, err
	}
	batch, err := e.BeginBatch(ctx, change.VaultRoot)
	if err != nil {
		return Outcome{PluginID: change.PluginID}, err
	}
	defer batch.Close()
	if runtime != nil {
		return batch.ApplyLive(ctx, change, runtime)
	}
	return batch.Apply(ctx, change)
}

func (e *Engine) applyLocked(ctx context.Context, change PreparedChange, runtime RuntimeSession, batchRoot string) (Outcome, error) {
	outcome := Outcome{PluginID: change.PluginID}
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	paths, stagedManifest, err := validatePrepared(change)
	if err != nil {
		return outcome, err
	}
	if paths.root != batchRoot {
		return outcome, errors.New("prepared change targets a different Vault than the active Batch")
	}

	if _, err := recoverLocked(paths); err != nil {
		return outcome, err
	}
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	if err := validateCurrentState(change, paths); err != nil {
		return outcome, err
	}

	enabled, err := readEnabled(paths.enabled)
	if err != nil {
		return outcome, err
	}
	priorEnabled := contains(enabled, change.PluginID)
	priorPresent, _ := directoryPresence(paths.plugin)

	prepared, err := prepareReplacement(change, paths, priorPresent)
	if err != nil {
		return outcome, err
	}
	if prepared != "" {
		defer os.RemoveAll(prepared)
	}

	j := journal{
		Version: 1, PluginID: change.PluginID, Kind: change.Kind,
		PriorPresent: priorPresent, PriorEnabled: priorEnabled,
		Live: runtime != nil, Phase: phasePrepared,
	}
	if err := beginJournal(paths, j); err != nil {
		return outcome, err
	}

	rollbackCtx := context.WithoutCancel(ctx)
	if runtime != nil {
		if prepareErr := runtime.Prepare(ctx); prepareErr != nil {
			if rollbackErr := runtime.Rollback(rollbackCtx); rollbackErr != nil {
				cause := errors.Join(prepareErr, fmt.Errorf("restore runtime state: %w", rollbackErr))
				cause = markRuntimeRestoreRequired(paths, j, cause)
				return outcome, &RecoveryRequiredError{Path: paths.current, Err: cause}
			}
			if cleanupErr := clearRecovery(paths); cleanupErr != nil {
				return outcome, &RecoveryRequiredError{Path: paths.current, Err: errors.Join(prepareErr, cleanupErr)}
			}
			return outcome, prepareErr
		}
	}

	mutateErr := e.replace(change, paths, prepared, &j)
	if mutateErr == nil {
		mutateErr = setEnabled(paths.enabled, enabled, change.PluginID, desiredEnabled(change.Enabled, priorEnabled))
		if mutateErr == nil {
			j.Phase = phaseEnabledWritten
			mutateErr = writeJournal(paths.journal, j)
		}
	}
	if mutateErr == nil {
		mutateErr = verify(change, paths.plugin, stagedManifest, desiredEnabled(change.Enabled, priorEnabled), paths.enabled)
	}
	if mutateErr == nil && runtime != nil {
		mutateErr = runtime.Commit(rollbackCtx)
	}
	if mutateErr == nil {
		if err := clearRecovery(paths); err != nil {
			return outcome, &RecoveryRequiredError{Path: paths.current, Err: fmt.Errorf("remove completed recovery record: %w", err)}
		}
		outcome.Changed = true
		return outcome, nil
	}

	if restoreErr := restoreFiles(paths, j); restoreErr != nil {
		return outcome, &RecoveryRequiredError{Path: paths.current, Err: errors.Join(mutateErr, restoreErr)}
	}
	outcome.Restored = true
	if runtime != nil {
		if runtimeErr := runtime.Rollback(rollbackCtx); runtimeErr != nil {
			cause := errors.Join(mutateErr, fmt.Errorf("restore runtime state: %w", runtimeErr))
			cause = markRuntimeRestoreRequired(paths, j, cause)
			return outcome, &RecoveryRequiredError{Path: paths.current, Err: cause}
		}
	}
	if cleanupErr := clearRecovery(paths); cleanupErr != nil {
		return outcome, &RecoveryRequiredError{Path: paths.current, Err: errors.Join(mutateErr, cleanupErr)}
	}
	return outcome, mutateErr
}

// Recover restores an interrupted transition without applying another change.
func (e *Engine) Recover(ctx context.Context, vaultRoot string) (RecoveryOutcome, error) {
	batch, err := e.BeginBatch(ctx, vaultRoot)
	if err != nil {
		return RecoveryOutcome{}, err
	}
	defer batch.Close()
	return batch.Recover(ctx)
}

func (e *Engine) replace(change PreparedChange, paths vaultPaths, prepared string, j *journal) error {
	if j.PriorPresent {
		if err := os.MkdirAll(filepath.Dir(paths.restorePlugin), 0o700); err != nil {
			return fmt.Errorf("create Restore Point: %w", err)
		}
		if err := os.Rename(paths.plugin, paths.restorePlugin); err != nil {
			return fmt.Errorf("move prior plugin to Restore Point: %w", err)
		}
		j.Phase = phasePriorMoved
		if err := writeJournal(paths.journal, *j); err != nil {
			return err
		}
	}
	if prepared != "" {
		if err := os.Rename(prepared, paths.plugin); err != nil {
			return fmt.Errorf("atomically install prepared plugin: %w", err)
		}
	}
	j.Phase = phaseReplaced
	if err := writeJournal(paths.journal, *j); err != nil {
		return err
	}
	if e.afterReplacement != nil {
		return e.afterReplacement()
	}
	return nil
}

type vaultPaths struct {
	root, obsidian, plugins, plugin, enabled, plugman, current, journal, restorePlugin, staging, lock string
}

func vaultPathsFor(root, pluginID string) (vaultPaths, error) {
	if root == "" {
		return vaultPaths{}, errors.New("Vault Root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return vaultPaths{}, fmt.Errorf("resolve Vault Root: %w", err)
	}
	obsidian := filepath.Join(abs, ".obsidian")
	info, err := os.Lstat(obsidian)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return vaultPaths{}, fmt.Errorf("Vault Root must contain a regular .obsidian directory")
	}
	plugman := filepath.Join(obsidian, ".plugman")
	for _, candidate := range []string{filepath.Join(obsidian, "plugins"), plugman} {
		if info, statErr := os.Lstat(candidate); statErr == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return vaultPaths{}, fmt.Errorf("managed path %s must be a regular directory", candidate)
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return vaultPaths{}, fmt.Errorf("inspect managed path %s: %w", candidate, statErr)
		}
	}
	current := filepath.Join(plugman, "recovery", "current")
	plugins := filepath.Join(obsidian, "plugins")
	return vaultPaths{
		root: abs, obsidian: obsidian, plugins: plugins, plugin: filepath.Join(plugins, pluginID),
		enabled: filepath.Join(obsidian, "community-plugins.json"), plugman: plugman,
		current: current, journal: filepath.Join(current, "journal.json"),
		restorePlugin: filepath.Join(current, "restore", "plugin"), staging: filepath.Join(plugman, "staging"),
		lock: filepath.Join(plugman, "operation.lock"),
	}, nil
}

type manifest struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

func validatePrepared(change PreparedChange) (vaultPaths, manifest, error) {
	var m manifest
	if !validPluginID(change.PluginID) {
		return vaultPaths{}, m, fmt.Errorf("invalid plugin ID %q", change.PluginID)
	}
	if change.Kind != Install && change.Kind != Update && change.Kind != Uninstall {
		return vaultPaths{}, m, fmt.Errorf("invalid change kind %q", change.Kind)
	}
	paths, err := vaultPathsFor(change.VaultRoot, change.PluginID)
	if err != nil {
		return paths, m, err
	}
	if change.SourceRecord != nil {
		if change.Kind == Uninstall || !provenance.ValidGitHubSource(change.SourceRecord.Repository, change.SourceRecord.Release) {
			return paths, m, fmt.Errorf("invalid GitHub Source Record")
		}
	}
	if change.Kind == Uninstall {
		return paths, m, nil
	}
	info, err := os.Lstat(change.StagedDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return paths, m, fmt.Errorf("staged plugin must be a regular directory")
	}
	for _, name := range []string{"main.js", "manifest.json"} {
		if err := requireRegular(filepath.Join(change.StagedDir, name)); err != nil {
			return paths, m, fmt.Errorf("validate staged %s: %w", name, err)
		}
	}
	for _, name := range []string{"styles.css", ".plugman.json"} {
		if err := optionalRegular(filepath.Join(change.StagedDir, name)); err != nil {
			return paths, m, fmt.Errorf("validate staged %s: %w", name, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(change.StagedDir, "manifest.json"))
	if err != nil || json.Unmarshal(data, &m) != nil || m.ID != change.PluginID || m.Version == "" {
		return paths, manifest{}, fmt.Errorf("staged manifest must have ID %q and a non-empty version", change.PluginID)
	}
	return paths, m, nil
}

func validateCurrentState(change PreparedChange, paths vaultPaths) error {
	if err := validateTarget(change, paths.plugin); err != nil {
		return err
	}
	if _, err := readEnabled(paths.enabled); err != nil {
		return err
	}
	present, err := directoryPresence(paths.plugin)
	if err != nil {
		return err
	}
	if change.Kind == Install && present {
		return fmt.Errorf("install %q: plugin is already installed", change.PluginID)
	}
	if (change.Kind == Update || change.Kind == Uninstall) && !present {
		return fmt.Errorf("%s %q: plugin is not installed", change.Kind, change.PluginID)
	}
	if present && change.Kind == Update {
		if err := optionalRegular(filepath.Join(paths.plugin, "data.json")); err != nil {
			return fmt.Errorf("preserve data.json: %w", err)
		}
	}
	return nil
}

func validPluginID(id string) bool {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return false
	}
	for index, r := range id {
		letterOrDigit := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if !letterOrDigit && !(index > 0 && (r == '-' || r == '_' || r == '.')) {
			return false
		}
	}
	return true
}

func validateTarget(change PreparedChange, target string) error {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect plugin target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("plugin target %q must not be a symbolic link", change.PluginID)
	}
	if !info.IsDir() {
		return fmt.Errorf("plugin target %q must be a directory", change.PluginID)
	}
	return nil
}

func prepareReplacement(change PreparedChange, paths vaultPaths, priorPresent bool) (string, error) {
	if change.Kind == Uninstall && !change.KeepData {
		return "", nil
	}
	if err := os.MkdirAll(paths.staging, 0o700); err != nil {
		return "", fmt.Errorf("create same-filesystem staging: %w", err)
	}
	prepared, err := os.MkdirTemp(paths.staging, change.PluginID+"-")
	if err != nil {
		return "", fmt.Errorf("create prepared plugin: %w", err)
	}
	fail := func(err error) (string, error) { os.RemoveAll(prepared); return "", err }
	if change.Kind != Uninstall {
		for _, name := range []string{"main.js", "manifest.json", "styles.css"} {
			src := filepath.Join(change.StagedDir, name)
			if err := optionalRegular(src); err != nil {
				return fail(err)
			}
			if _, err := os.Stat(src); err == nil {
				if err := copyRegular(src, filepath.Join(prepared, name)); err != nil {
					return fail(err)
				}
			}
		}
		if change.SourceRecord != nil {
			data, err := json.Marshal(change.SourceRecord)
			if err != nil {
				return fail(fmt.Errorf("encode Source Record: %w", err))
			}
			if err := os.WriteFile(filepath.Join(prepared, ".plugman.json"), append(data, '\n'), 0o600); err != nil {
				return fail(fmt.Errorf("write Source Record: %w", err))
			}
		}
	}
	copiedData := false
	if priorPresent && (change.Kind == Update || change.KeepData) {
		data := filepath.Join(paths.plugin, "data.json")
		if err := optionalRegular(data); err != nil {
			return fail(fmt.Errorf("preserve data.json: %w", err))
		}
		if _, err := os.Stat(data); err == nil {
			if err := copyRegular(data, filepath.Join(prepared, "data.json")); err != nil {
				return fail(fmt.Errorf("preserve data.json: %w", err))
			}
			copiedData = true
		}
	}
	if change.Kind == Uninstall && !copiedData {
		if err := os.Remove(prepared); err != nil {
			return fail(fmt.Errorf("remove empty prepared plugin: %w", err))
		}
		return "", nil
	}
	return prepared, nil
}

func requireRegular(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("must be a regular file")
	}
	return nil
}

func optionalRegular(path string) error {
	err := requireRegular(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func copyRegular(src, dst string) error {
	if err := requireRegular(src); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(src)
	if err != nil || !os.SameFile(info, pathInfo) {
		return errors.New("source changed while it was being opened")
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	return errors.Join(copyErr, syncErr, closeErr)
}

func directoryPresence(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("plugin target is not a regular directory")
	}
	return true, nil
}

func desiredEnabled(d EnabledDirective, prior bool) bool {
	switch d {
	case Enable:
		return true
	case Disable:
		return false
	default:
		return prior
	}
}

func verify(change PreparedChange, plugin string, staged manifest, enabled bool, enabledPath string) error {
	if change.Kind == Uninstall {
		if change.KeepData {
			for _, name := range []string{"main.js", "manifest.json", "styles.css", ".plugman.json"} {
				if _, err := os.Lstat(filepath.Join(plugin, name)); !os.IsNotExist(err) {
					return fmt.Errorf("verify uninstall: %s remains", name)
				}
			}
		} else if _, err := os.Lstat(plugin); !os.IsNotExist(err) {
			return fmt.Errorf("verify uninstall: plugin directory remains")
		}
	} else {
		data, err := os.ReadFile(filepath.Join(plugin, "manifest.json"))
		var got manifest
		if err != nil || json.Unmarshal(data, &got) != nil || got != staged {
			return fmt.Errorf("verify installed manifest")
		}
		if err := requireRegular(filepath.Join(plugin, "main.js")); err != nil {
			return fmt.Errorf("verify installed main.js: %w", err)
		}
	}
	ids, err := readEnabled(enabledPath)
	if err != nil || contains(ids, change.PluginID) != enabled {
		return fmt.Errorf("verify enabled state: %w", err)
	}
	return nil
}

func readEnabled(path string) ([]string, error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("enabled plugin configuration must be a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect enabled plugins: %w", err)
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read enabled plugins: %w", err)
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, fmt.Errorf("read enabled plugins: %w", err)
	}
	return ids, nil
}

func setEnabled(path string, current []string, id string, wanted bool) error {
	next := make([]string, 0, len(current)+1)
	found := false
	for _, item := range current {
		if item == id {
			if wanted && !found {
				next = append(next, item)
				found = true
			}
			continue
		}
		next = append(next, item)
	}
	if wanted && !found {
		next = append(next, id)
	}
	return atomicJSON(path, next)
}

func contains(ids []string, id string) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}

func atomicJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".plugman-json-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
