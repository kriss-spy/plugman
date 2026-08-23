package change

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyUpdatePreservesDataAndRemovesStaleStyles(t *testing.T) {
	vault := newVault(t, []string{"demo"})
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	writePlugin(t, plugin, "demo", "1.0.0", true)
	if err := os.WriteFile(filepath.Join(plugin, "data.json"), []byte(`{"kept":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(vault, "download")
	writePlugin(t, stage, "demo", "2.0.0", false)

	outcome, err := New().Apply(context.Background(), PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: Update, StagedDir: stage, Enabled: PreserveEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Changed || outcome.Restored {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	assertFile(t, filepath.Join(plugin, "data.json"), `{"kept":true}`)
	assertManifestVersion(t, plugin, "2.0.0")
	if _, err := os.Stat(filepath.Join(plugin, "styles.css")); !os.IsNotExist(err) {
		t.Fatalf("stale styles.css was not removed: %v", err)
	}
	assertEnabled(t, vault, []string{"demo"})
	assertNoRecovery(t, vault)
}

func TestApplyInstallCanEnablePlugin(t *testing.T) {
	vault := newVault(t, nil)
	stage := filepath.Join(vault, "download")
	writePlugin(t, stage, "demo", "1.0.0", true)
	_, err := New().Apply(context.Background(), PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: Install, StagedDir: stage, Enabled: Enable,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertEnabled(t, vault, []string{"demo"})
	assertManifestVersion(t, filepath.Join(vault, ".obsidian", "plugins", "demo"), "1.0.0")
}

func TestApplyPersistsGitHubSourceRecord(t *testing.T) {
	vault := newVault(t, nil)
	stage := filepath.Join(vault, "download")
	writePlugin(t, stage, "demo", "1.2.3", false)
	record := &SourceRecord{Repository: "https://github.com/owner/demo", Release: "1.2.3"}

	_, err := New().Apply(context.Background(), PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: Install, StagedDir: stage, SourceRecord: record,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(vault, ".obsidian", "plugins", "demo", ".plugman.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got SourceRecord
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != *record {
		t.Fatalf("Source Record = %#v, want %#v", got, *record)
	}
}

func TestValidateRejectsNonCanonicalGitHubSourceRecord(t *testing.T) {
	tests := []SourceRecord{
		{Repository: "http://github.com/owner/demo", Release: "1.0.0"},
		{Repository: "https://github.com.evil/owner/demo", Release: "1.0.0"},
		{Repository: "https://user@github.com/owner/demo", Release: "1.0.0"},
		{Repository: "https://github.com/owner/demo?ref=main", Release: "1.0.0"},
		{Repository: "https://github.com/owner/demo#readme", Release: "1.0.0"},
		{Repository: "https://github.com/owner/demo/releases", Release: "1.0.0"},
		{Repository: "https://github.com/owner/demo/", Release: "1.0.0"},
		{Repository: "https://github.com/owner", Release: "1.0.0"},
		{Repository: "https://github.com/owner%2Fdemo/repo", Release: "1.0.0"},
		{Repository: "https://github.com/owner/demo", Release: ""},
		{Repository: "https://github.com/owner/demo", Release: "feature/release"},
		{Repository: "https://github.com/owner/demo", Release: `..\release`},
		{Repository: "https://github.com/owner/demo", Release: ".."},
		{Repository: "https://github.com/owner/demo", Release: " 1.0.0"},
	}
	for _, record := range tests {
		t.Run(record.Repository+"@"+record.Release, func(t *testing.T) {
			vault := newVault(t, nil)
			stage := filepath.Join(vault, "stage")
			writePlugin(t, stage, "demo", "1.0.0", true)
			err := New().Validate(context.Background(), PreparedChange{
				VaultRoot: vault, PluginID: "demo", Kind: Install, StagedDir: stage, SourceRecord: &record,
			})
			if err == nil {
				t.Fatalf("Source Record accepted: %+v", record)
			}
		})
	}
}

func TestApplyUninstallRemovesFolderOrKeepsOnlyData(t *testing.T) {
	for _, keepData := range []bool{false, true} {
		t.Run(map[bool]string{false: "remove", true: "keep data"}[keepData], func(t *testing.T) {
			vault := newVault(t, []string{"demo", "other"})
			plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
			writePlugin(t, plugin, "demo", "1.0.0", true)
			if err := os.WriteFile(filepath.Join(plugin, "data.json"), []byte("settings"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := New().Apply(context.Background(), PreparedChange{
				VaultRoot: vault, PluginID: "demo", Kind: Uninstall, KeepData: keepData, Enabled: Disable,
			})
			if err != nil {
				t.Fatal(err)
			}
			if keepData {
				assertFile(t, filepath.Join(plugin, "data.json"), "settings")
				if _, err := os.Stat(filepath.Join(plugin, "main.js")); !os.IsNotExist(err) {
					t.Fatalf("main.js remains: %v", err)
				}
			} else if _, err := os.Stat(plugin); !os.IsNotExist(err) {
				t.Fatalf("plugin folder remains: %v", err)
			}
			assertEnabled(t, vault, []string{"other"})
		})
	}
}

func TestApplyKeepDataWithoutDataRemovesFolder(t *testing.T) {
	vault := newVault(t, nil)
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	writePlugin(t, plugin, "demo", "1.0.0", true)
	_, err := New().Apply(context.Background(), PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: Uninstall, KeepData: true, Enabled: Disable,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plugin); !os.IsNotExist(err) {
		t.Fatalf("plugin folder remains without data: %v", err)
	}
}

func TestApplyRestoresControlledFailure(t *testing.T) {
	vault := newVault(t, []string{"demo"})
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	writePlugin(t, plugin, "demo", "1.0.0", true)
	stage := filepath.Join(vault, "download")
	writePlugin(t, stage, "demo", "2.0.0", true)
	want := errors.New("simulated failure")
	engine := New()
	engine.afterReplacement = func() error { return want }
	outcome, err := engine.Apply(context.Background(), PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: Update, StagedDir: stage, Enabled: PreserveEnabled,
	})
	if !errors.Is(err, want) || !outcome.Restored {
		t.Fatalf("got outcome=%+v err=%v", outcome, err)
	}
	assertManifestVersion(t, plugin, "1.0.0")
	assertEnabled(t, vault, []string{"demo"})
	assertNoRecovery(t, vault)
}

func TestApplyLiveRetainsRecoveryWhenRuntimeRollbackCannotBeVerified(t *testing.T) {
	vault := newVault(t, []string{"demo"})
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	writePlugin(t, plugin, "demo", "1.0.0", true)
	stage := filepath.Join(vault, "download")
	writePlugin(t, stage, "demo", "2.0.0", true)
	runtime := &failingRuntimeSession{
		commitErr:   errors.New("reload failed"),
		rollbackErr: errors.New("restored runtime cannot be verified"),
	}

	outcome, err := New().ApplyLive(context.Background(), PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: Update, StagedDir: stage, Enabled: PreserveEnabled,
	}, runtime)
	var recoveryErr *RecoveryRequiredError
	if !errors.As(err, &recoveryErr) {
		t.Fatalf("error = %v, want RecoveryRequiredError", err)
	}
	if outcome.Changed || !outcome.Restored {
		t.Fatalf("outcome = %+v", outcome)
	}
	assertManifestVersion(t, plugin, "1.0.0")
	assertEnabled(t, vault, []string{"demo"})
	if _, statErr := os.Stat(recoveryErr.Path); statErr != nil {
		t.Fatalf("recovery marker missing: %v", statErr)
	}
	if _, recoverErr := New().Recover(context.Background(), vault); !errors.As(recoverErr, &recoveryErr) {
		t.Fatalf("Recover error = %v, want RecoveryRequiredError", recoverErr)
	}
}

func TestApplyLiveRetainsRecoveryWhenPrepareRollbackCannotBeVerified(t *testing.T) {
	vault := newVault(t, []string{"demo"})
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	writePlugin(t, plugin, "demo", "1.0.0", true)
	stage := filepath.Join(vault, "download")
	writePlugin(t, stage, "demo", "2.0.0", true)
	runtime := &failingRuntimeSession{
		prepareErr:  errors.New("disable result unknown"),
		rollbackErr: errors.New("prior runtime cannot be verified"),
	}

	outcome, err := New().ApplyLive(context.Background(), PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: Update, StagedDir: stage, Enabled: PreserveEnabled,
	}, runtime)
	var recoveryErr *RecoveryRequiredError
	if !errors.As(err, &recoveryErr) {
		t.Fatalf("error = %v, want RecoveryRequiredError", err)
	}
	if outcome.Changed || outcome.Restored {
		t.Fatalf("outcome = %+v", outcome)
	}
	assertManifestVersion(t, plugin, "1.0.0")
	assertEnabled(t, vault, []string{"demo"})
	if _, statErr := os.Stat(recoveryErr.Path); statErr != nil {
		t.Fatalf("recovery marker missing: %v", statErr)
	}
}

type failingRuntimeSession struct {
	prepareErr  error
	commitErr   error
	rollbackErr error
}

func (s *failingRuntimeSession) Prepare(context.Context) error  { return s.prepareErr }
func (s *failingRuntimeSession) Commit(context.Context) error   { return s.commitErr }
func (s *failingRuntimeSession) Rollback(context.Context) error { return s.rollbackErr }

func TestApplyRecoversInterruptedReplacementBeforeMutation(t *testing.T) {
	vault := newVault(t, []string{"demo"})
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	writePlugin(t, plugin, "demo", "2.0.0", false)
	recovery := filepath.Join(vault, ".obsidian", ".plugman", "recovery", "current")
	restore := filepath.Join(recovery, "restore", "plugin")
	writePlugin(t, restore, "demo", "1.0.0", true)
	writeJSON(t, filepath.Join(recovery, "journal.json"), journal{
		Version: 1, PluginID: "demo", Kind: Update, PriorPresent: true, PriorEnabled: true, Phase: phaseReplaced,
	})
	stage := filepath.Join(vault, "next")
	writePlugin(t, stage, "other", "1.0.0", true)

	_, err := New().Apply(context.Background(), PreparedChange{
		VaultRoot: vault, PluginID: "other", Kind: Install, StagedDir: stage, Enabled: PreserveEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertManifestVersion(t, plugin, "1.0.0")
	assertEnabled(t, vault, []string{"demo"})
	assertManifestVersion(t, filepath.Join(vault, ".obsidian", "plugins", "other"), "1.0.0")
}

func TestRecoverInterruptedLiveReplacementRestoresDiskBeforeRequiringRuntimeRecovery(t *testing.T) {
	vault := newVault(t, nil)
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	writePlugin(t, plugin, "demo", "2.0.0", false)
	recovery := filepath.Join(vault, ".obsidian", ".plugman", "recovery", "current")
	restore := filepath.Join(recovery, "restore", "plugin")
	writePlugin(t, restore, "demo", "1.0.0", true)
	writeJSON(t, filepath.Join(recovery, "journal.json"), journal{
		Version: 1, PluginID: "demo", Kind: Update, PriorPresent: true,
		PriorEnabled: true, Live: true, Phase: phaseReplaced,
	})

	outcome, err := New().Recover(context.Background(), vault)
	var recoveryErr *RecoveryRequiredError
	if !errors.As(err, &recoveryErr) {
		t.Fatalf("Recover error = %v, want RecoveryRequiredError", err)
	}
	if outcome.Recovered {
		t.Fatalf("outcome = %+v", outcome)
	}
	assertManifestVersion(t, plugin, "1.0.0")
	assertEnabled(t, vault, []string{"demo"})
	data, readErr := os.ReadFile(filepath.Join(recovery, "journal.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var got journal
	if json.Unmarshal(data, &got) != nil || got.Phase != phaseRuntimeRestoreRequired {
		t.Fatalf("journal = %+v, want runtime recovery marker", got)
	}
	if _, secondErr := New().Recover(context.Background(), vault); !errors.As(secondErr, &recoveryErr) {
		t.Fatalf("second Recover error = %v, want RecoveryRequiredError", secondErr)
	}
}

func TestRecoverInterruptedLiveChangeRestoresEveryCrashPhase(t *testing.T) {
	for _, test := range []struct {
		name          string
		phase         phase
		targetVersion string
		restore       bool
	}{
		{name: "prepared", phase: phasePrepared, targetVersion: "1.0.0"},
		{name: "prior moved", phase: phasePriorMoved, restore: true},
		{name: "enabled written", phase: phaseEnabledWritten, targetVersion: "2.0.0", restore: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			vault := newVault(t, nil)
			plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
			if test.targetVersion != "" {
				writePlugin(t, plugin, "demo", test.targetVersion, true)
			}
			recovery := filepath.Join(vault, ".obsidian", ".plugman", "recovery", "current")
			if test.restore {
				writePlugin(t, filepath.Join(recovery, "restore", "plugin"), "demo", "1.0.0", true)
			}
			writeJSON(t, filepath.Join(recovery, "journal.json"), journal{
				Version: 1, PluginID: "demo", Kind: Update, PriorPresent: true,
				PriorEnabled: true, Live: true, Phase: test.phase,
			})

			_, err := New().Recover(context.Background(), vault)
			var recoveryErr *RecoveryRequiredError
			if !errors.As(err, &recoveryErr) {
				t.Fatalf("Recover error = %v, want RecoveryRequiredError", err)
			}
			assertManifestVersion(t, plugin, "1.0.0")
			assertEnabled(t, vault, []string{"demo"})
		})
	}
}

func TestApplyRejectsUnsafeIDAndSymlinkBeforeMutation(t *testing.T) {
	vault := newVault(t, nil)
	stage := filepath.Join(vault, "download")
	writePlugin(t, stage, "demo", "1.0.0", true)
	if _, err := New().Apply(context.Background(), PreparedChange{VaultRoot: vault, PluginID: "../demo", Kind: Install, StagedDir: stage}); err == nil {
		t.Fatal("expected traversal rejection")
	}
	target := filepath.Join(vault, ".obsidian", "plugins", "demo")
	if err := os.Symlink(t.TempDir(), target); err != nil {
		t.Fatal(err)
	}
	if _, err := New().Apply(context.Background(), PreparedChange{VaultRoot: vault, PluginID: "demo", Kind: Install, StagedDir: stage}); err == nil {
		t.Fatal("expected plugin symlink rejection")
	}
}

func TestApplyRejectsConcurrentVaultMutation(t *testing.T) {
	vault := newVault(t, nil)
	paths, err := vaultPathsFor(vault, "demo")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := acquireVaultLock(paths.lock)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	stage := filepath.Join(vault, "download")
	writePlugin(t, stage, "demo", "1.0.0", true)
	if _, err := New().Apply(context.Background(), PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: Install, StagedDir: stage,
	}); err == nil {
		t.Fatal("expected exclusive lock error")
	}
}

func TestRecoverRetainsInvalidJournal(t *testing.T) {
	vault := newVault(t, nil)
	current := filepath.Join(vault, ".obsidian", ".plugman", "recovery", "current")
	if err := os.MkdirAll(current, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "journal.json"), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New().Recover(context.Background(), vault)
	var recoveryErr *RecoveryRequiredError
	if !errors.As(err, &recoveryErr) {
		t.Fatalf("expected RecoveryRequiredError, got %v", err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("recovery material was not retained: %v", err)
	}
}

func TestRecoverRejectsImpossibleJournalWithoutMutatingVault(t *testing.T) {
	tests := []struct {
		name    string
		journal journal
	}{
		{name: "unknown kind", journal: journal{Version: 1, PluginID: "demo", Kind: Kind("garbage"), Phase: phasePrepared}},
		{name: "unknown phase", journal: journal{Version: 1, PluginID: "demo", Kind: Install, Phase: phase("garbage")}},
		{name: "prior moved without prior plugin", journal: journal{Version: 1, PluginID: "demo", Kind: Install, Phase: phasePriorMoved}},
		{name: "runtime marker without live change", journal: journal{Version: 1, PluginID: "demo", Kind: Install, Phase: phaseRuntimeRestoreRequired}},
		{name: "install claiming prior plugin", journal: journal{Version: 1, PluginID: "demo", Kind: Install, PriorPresent: true, Phase: phasePrepared}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vault := newVault(t, []string{"demo"})
			plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
			writePlugin(t, plugin, "demo", "1.0.0", true)
			current := filepath.Join(vault, ".obsidian", ".plugman", "recovery", "current")
			writeJSON(t, filepath.Join(current, "journal.json"), test.journal)

			_, err := New().Recover(context.Background(), vault)
			var recoveryErr *RecoveryRequiredError
			if !errors.As(err, &recoveryErr) {
				t.Fatalf("Recover error = %v, want RecoveryRequiredError", err)
			}
			assertManifestVersion(t, plugin, "1.0.0")
			assertEnabled(t, vault, []string{"demo"})
			if _, statErr := os.Stat(current); statErr != nil {
				t.Fatalf("recovery material was removed: %v", statErr)
			}
		})
	}
}

func newVault(t *testing.T, enabled []string) string {
	t.Helper()
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, ".obsidian", "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(vault, ".obsidian", "community-plugins.json"), enabled)
	return vault
}

func writePlugin(t *testing.T, dir, id, version string, styles bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(dir, "manifest.json"), map[string]string{"id": id, "version": version})
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte("plugin"), 0o600); err != nil {
		t.Fatal(err)
	}
	if styles {
		if err := os.WriteFile(filepath.Join(dir, "styles.css"), []byte("css"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertManifestVersion(t *testing.T, plugin, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(plugin, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Version != want {
		t.Fatalf("manifest version = %q, err=%v, want %q", manifest.Version, err, want)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("file %s = %q, err=%v, want %q", path, data, err, want)
	}
}

func assertEnabled(t *testing.T, vault string, want []string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(vault, ".obsidian", "community-plugins.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if stringJSON(got) != stringJSON(want) {
		t.Fatalf("enabled = %v, want %v", got, want)
	}
}

func stringJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func assertNoRecovery(t *testing.T, vault string) {
	t.Helper()
	path := filepath.Join(vault, ".obsidian", ".plugman", "recovery", "current")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("recovery remains at %s: %v", path, err)
	}
}
