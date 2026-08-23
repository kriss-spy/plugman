package change

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type phase string

const (
	phasePrepared       phase = "prepared"
	phasePriorMoved     phase = "prior-moved"
	phaseReplaced       phase = "replaced"
	phaseEnabledWritten phase = "enabled-written"
	// phaseRuntimeRestoreRequired means the prior files and enabled config were
	// restored, but the live runtime could not be restored and verified. Closed
	// recovery must preserve this marker for live or manual intervention.
	phaseRuntimeRestoreRequired phase = "runtime-restore-required"
)

type journal struct {
	Version      int    `json:"version"`
	PluginID     string `json:"pluginId"`
	Kind         Kind   `json:"kind"`
	PriorPresent bool   `json:"priorPresent"`
	PriorEnabled bool   `json:"priorEnabled"`
	Live         bool   `json:"live,omitempty"`
	Phase        phase  `json:"phase"`
}

func beginJournal(paths vaultPaths, j journal) error {
	if _, err := os.Lstat(paths.current); err == nil {
		return &RecoveryRequiredError{Path: paths.current, Err: errors.New("recovery journal already exists")}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(paths.current, 0o700); err != nil {
		return fmt.Errorf("create recovery journal: %w", err)
	}
	if err := writeJournal(paths.journal, j); err != nil {
		_ = os.RemoveAll(paths.current)
		return &RecoveryRequiredError{Path: paths.current, Err: err}
	}
	return nil
}

func writeJournal(path string, j journal) error {
	if err := atomicJSON(path, j); err != nil {
		return fmt.Errorf("write recovery journal: %w", err)
	}
	return nil
}

func recoverLocked(paths vaultPaths) (RecoveryOutcome, error) {
	var outcome RecoveryOutcome
	info, err := os.Lstat(paths.current)
	if os.IsNotExist(err) {
		return outcome, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return outcome, &RecoveryRequiredError{Path: paths.current, Err: errors.New("recovery path is not a regular directory")}
	}
	data, err := os.ReadFile(paths.journal)
	if err != nil {
		return outcome, &RecoveryRequiredError{Path: paths.current, Err: fmt.Errorf("read journal: %w", err)}
	}
	var j journal
	if err := json.Unmarshal(data, &j); err != nil || !validJournal(j) {
		return outcome, &RecoveryRequiredError{Path: paths.current, Err: errors.New("invalid recovery journal")}
	}
	pluginPaths, err := vaultPathsFor(paths.root, j.PluginID)
	if err != nil {
		return outcome, &RecoveryRequiredError{Path: paths.current, Err: err}
	}
	if j.Live {
		if j.Phase == phaseRuntimeRestoreRequired {
			return outcome, &RecoveryRequiredError{Path: paths.current, Err: errors.New("live runtime restoration has not been verified")}
		}
		if err := restoreFiles(pluginPaths, j); err != nil {
			return outcome, &RecoveryRequiredError{Path: paths.current, Err: fmt.Errorf("restore interrupted live change files: %w", err)}
		}
		cause := errors.New("interrupted live change files restored; runtime restoration has not been verified")
		cause = markRuntimeRestoreRequired(pluginPaths, j, cause)
		return outcome, &RecoveryRequiredError{Path: paths.current, Err: cause}
	}
	if err := restore(pluginPaths, j); err != nil {
		return outcome, &RecoveryRequiredError{Path: paths.current, Err: err}
	}
	return RecoveryOutcome{Recovered: true, PluginID: j.PluginID}, nil
}

func validJournal(j journal) bool {
	if j.Version != 1 || !validPluginID(j.PluginID) {
		return false
	}
	switch j.Kind {
	case Install:
		if j.PriorPresent {
			return false
		}
	case Update, Uninstall:
		if !j.PriorPresent {
			return false
		}
	default:
		return false
	}
	switch j.Phase {
	case phasePrepared, phaseReplaced, phaseEnabledWritten:
		return true
	case phasePriorMoved:
		return j.PriorPresent
	case phaseRuntimeRestoreRequired:
		return j.Live
	default:
		return false
	}
}

func restore(paths vaultPaths, j journal) error {
	if err := restoreFiles(paths, j); err != nil {
		return err
	}
	return clearRecovery(paths)
}

func restoreFiles(paths vaultPaths, j journal) error {
	targetInfo, targetErr := os.Lstat(paths.plugin)
	if targetErr == nil && targetInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("refuse to restore over plugin symlink")
	}
	restoreInfo, restoreErr := os.Lstat(paths.restorePlugin)
	restoreExists := restoreErr == nil && restoreInfo.IsDir() && restoreInfo.Mode()&os.ModeSymlink == 0
	if restoreErr != nil && !os.IsNotExist(restoreErr) {
		return fmt.Errorf("inspect Restore Point: %w", restoreErr)
	}

	if j.PriorPresent {
		if restoreExists {
			if targetErr == nil {
				if err := os.RemoveAll(paths.plugin); err != nil {
					return fmt.Errorf("remove interrupted plugin: %w", err)
				}
			} else if !os.IsNotExist(targetErr) {
				return targetErr
			}
			if err := os.MkdirAll(paths.plugins, 0o700); err != nil {
				return err
			}
			if err := os.Rename(paths.restorePlugin, paths.plugin); err != nil {
				return fmt.Errorf("restore prior plugin: %w", err)
			}
		} else if j.Phase != phasePrepared || targetErr != nil {
			return errors.New("prior plugin Restore Point is missing")
		}
	} else if targetErr == nil {
		if err := os.RemoveAll(paths.plugin); err != nil {
			return fmt.Errorf("remove interrupted install: %w", err)
		}
	} else if !os.IsNotExist(targetErr) {
		return targetErr
	}

	ids, err := readEnabled(paths.enabled)
	if err != nil {
		return err
	}
	if err := setEnabled(paths.enabled, ids, j.PluginID, j.PriorEnabled); err != nil {
		return fmt.Errorf("restore enabled state: %w", err)
	}
	if j.PriorPresent {
		if present, err := directoryPresence(paths.plugin); err != nil || !present {
			return errors.New("restored plugin cannot be verified")
		}
	} else if _, err := os.Lstat(paths.plugin); !os.IsNotExist(err) {
		return errors.New("removed interrupted install cannot be verified")
	}
	ids, err = readEnabled(paths.enabled)
	if err != nil || contains(ids, j.PluginID) != j.PriorEnabled {
		return errors.New("restored enabled state cannot be verified")
	}
	return nil
}

func clearRecovery(paths vaultPaths) error {
	if err := os.RemoveAll(paths.current); err != nil {
		return fmt.Errorf("remove recovery material: %w", err)
	}
	removeEmpty(filepath.Dir(paths.current))
	return nil
}

func markRuntimeRestoreRequired(paths vaultPaths, j journal, cause error) error {
	j.Phase = phaseRuntimeRestoreRequired
	if err := writeJournal(paths.journal, j); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func removeEmpty(path string) { _ = os.Remove(path) }
