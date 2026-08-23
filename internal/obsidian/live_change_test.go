package obsidian_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kriss-spy/plugman/internal/change"
	"github.com/kriss-spy/plugman/internal/obsidian"
)

func TestLiveChangePreservesPriorEnabledState(t *testing.T) {
	vault := liveVault(t, []string{"demo"})
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	livePlugin(t, plugin, "1.0.0")
	stage := filepath.Join(vault, "stage")
	livePlugin(t, stage, "2.0.0")
	prior := obsidian.PluginState{Present: true, ID: "demo", Version: "1.0.0", Enabled: true, Loaded: true}
	client := newFakeClient(prior)
	client.onReload = func() {
		client.state = stateFromDisk(t, plugin, containsLive(enabledLive(t, vault), "demo"))
	}

	outcome, err := change.New().ApplyLive(context.Background(), change.PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: change.Update, StagedDir: stage,
		Enabled: change.PreserveEnabled,
	}, obsidian.NewCoordinator(client).Session(obsidian.ChangePlan{
		PluginID: "demo", PlannedState: prior, TargetVersion: "2.0.0",
	}))
	if err != nil {
		t.Fatalf("apply live change: %v", err)
	}
	if !outcome.Changed || outcome.Restored {
		t.Fatalf("outcome = %+v", outcome)
	}
	wantState := obsidian.PluginState{Present: true, ID: "demo", Version: "2.0.0", Enabled: true, Loaded: true}
	if client.state != wantState {
		t.Fatalf("runtime state = %+v, want %+v", client.state, wantState)
	}
	wantCalls := []string{"probe", "inspect", "inspect", "disable", "reload", "enable", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
	assertNoLiveRecovery(t, vault)
}

func TestLiveChangeInstallsNewPluginDisabled(t *testing.T) {
	vault := liveVault(t, nil)
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	stage := filepath.Join(vault, "stage")
	livePlugin(t, stage, "2.0.0")
	client := newFakeClient(obsidian.PluginState{})
	client.onReload = func() {
		client.state = stateFromDisk(t, plugin, containsLive(enabledLive(t, vault), "demo"))
	}

	outcome, err := change.New().ApplyLive(context.Background(), change.PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: change.Install, StagedDir: stage,
		Enabled: change.PreserveEnabled,
	}, obsidian.NewCoordinator(client).Session(obsidian.ChangePlan{
		PluginID: "demo", PlannedState: obsidian.PluginState{}, TargetVersion: "2.0.0",
	}))
	if err != nil {
		t.Fatalf("apply live change: %v", err)
	}
	if !outcome.Changed || client.state.Enabled || client.state.Loaded {
		t.Fatalf("outcome = %+v, runtime state = %+v", outcome, client.state)
	}
	wantCalls := []string{"probe", "inspect", "inspect", "reload", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
	assertNoLiveRecovery(t, vault)
}

func TestLiveChangeRejectsConcurrentRuntimeStateChangeBeforeDiskMutation(t *testing.T) {
	vault := liveVault(t, []string{"demo"})
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	livePlugin(t, plugin, "1.0.0")
	stage := filepath.Join(vault, "stage")
	livePlugin(t, stage, "2.0.0")
	prior := obsidian.PluginState{Present: true, ID: "demo", Version: "1.0.0", Enabled: true, Loaded: true}
	client := newFakeClient(prior)
	client.afterInspect = func(call int) {
		if call == 1 {
			client.state.Enabled = false
			client.state.Loaded = false
		}
	}

	outcome, err := change.New().ApplyLive(context.Background(), change.PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: change.Update, StagedDir: stage,
		Enabled: change.PreserveEnabled,
	}, obsidian.NewCoordinator(client).Session(obsidian.ChangePlan{
		PluginID: "demo", PlannedState: prior, TargetVersion: "2.0.0",
	}))
	if !errors.Is(err, obsidian.ErrStateChanged) {
		t.Fatalf("error = %v", err)
	}
	if outcome.Changed || outcome.Restored {
		t.Fatalf("outcome = %+v", outcome)
	}
	if got := stateFromDisk(t, plugin, true).Version; got != "1.0.0" {
		t.Fatalf("disk version = %q, want 1.0.0", got)
	}
	wantCalls := []string{"probe", "inspect", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
	assertNoLiveRecovery(t, vault)
}

func TestLiveChangeDoesNotMutateDiskWhenObsidianExitsDuringRecheck(t *testing.T) {
	vault := liveVault(t, []string{"demo"})
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	livePlugin(t, plugin, "1.0.0")
	stage := filepath.Join(vault, "stage")
	livePlugin(t, stage, "2.0.0")
	prior := obsidian.PluginState{Present: true, ID: "demo", Version: "1.0.0", Enabled: true, Loaded: true}
	client := newFakeClient(prior)
	client.inspectErrAt = 2
	client.inspectErr = obsidian.ErrObsidianNotRunning

	outcome, err := change.New().ApplyLive(context.Background(), change.PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: change.Update, StagedDir: stage,
		Enabled: change.PreserveEnabled,
	}, obsidian.NewCoordinator(client).Session(obsidian.ChangePlan{
		PluginID: "demo", PlannedState: prior, TargetVersion: "2.0.0",
	}))
	if !errors.Is(err, obsidian.ErrObsidianNotRunning) {
		t.Fatalf("error = %v", err)
	}
	if outcome.Changed || outcome.Restored {
		t.Fatalf("outcome = %+v", outcome)
	}
	if got := stateFromDisk(t, plugin, true).Version; got != "1.0.0" {
		t.Fatalf("disk version = %q, want 1.0.0", got)
	}
	wantCalls := []string{"probe", "inspect", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
	assertNoLiveRecovery(t, vault)
}

func TestLiveChangeRestoresDiskAndRuntimeWhenReloadFails(t *testing.T) {
	vault := liveVault(t, []string{"demo"})
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	livePlugin(t, plugin, "1.0.0")
	stage := filepath.Join(vault, "stage")
	livePlugin(t, stage, "2.0.0")

	prior := obsidian.PluginState{Present: true, ID: "demo", Version: "1.0.0", Enabled: true, Loaded: true}
	client := newFakeClient(prior)
	client.failOnce["reload"] = errors.New("runtime rejected replacement")
	client.onReload = func() {
		client.state = stateFromDisk(t, plugin, containsLive(enabledLive(t, vault), "demo"))
	}
	session := obsidian.NewCoordinator(client).Session(obsidian.ChangePlan{
		PluginID: "demo", PlannedState: prior, TargetVersion: "2.0.0",
	})

	outcome, err := change.New().ApplyLive(context.Background(), change.PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: change.Update, StagedDir: stage,
		Enabled: change.PreserveEnabled,
	}, session)
	if err == nil {
		t.Fatal("expected reload failure")
	}
	if !outcome.Restored || outcome.Changed {
		t.Fatalf("outcome = %+v", outcome)
	}
	if got := stateFromDisk(t, plugin, true); got != prior {
		t.Fatalf("restored disk state = %+v, want %+v", got, prior)
	}
	if client.state != prior {
		t.Fatalf("restored runtime state = %+v, want %+v", client.state, prior)
	}
	assertNoLiveRecovery(t, vault)
}

func TestLiveChangeUninstallsWithoutReloadingRemovedPlugin(t *testing.T) {
	vault := liveVault(t, []string{"demo"})
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	livePlugin(t, plugin, "1.0.0")
	prior := obsidian.PluginState{Present: true, ID: "demo", Version: "1.0.0", Enabled: true, Loaded: true}
	client := newFakeClient(prior)
	client.beforeInspect = func(call int) {
		if call == 3 {
			client.state = stateFromDisk(t, plugin, false)
		}
	}

	outcome, err := change.New().ApplyLive(context.Background(), change.PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: change.Uninstall, Enabled: change.Disable,
	}, obsidian.NewCoordinator(client).Session(obsidian.ChangePlan{
		PluginID: "demo", PlannedState: prior, TargetAbsent: true,
	}))
	if err != nil {
		t.Fatalf("apply live uninstall: %v", err)
	}
	if !outcome.Changed || outcome.Restored {
		t.Fatalf("outcome = %+v", outcome)
	}
	if client.state != (obsidian.PluginState{}) {
		t.Fatalf("runtime state = %+v, want absent", client.state)
	}
	if _, statErr := os.Stat(plugin); !os.IsNotExist(statErr) {
		t.Fatalf("plugin directory remains: %v", statErr)
	}
	wantCalls := []string{"probe", "inspect", "inspect", "disable", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
	assertNoLiveRecovery(t, vault)
}

func TestLiveChangeRestoresUninstallWhenRuntimeAbsenceCannotBeVerified(t *testing.T) {
	vault := liveVault(t, []string{"demo"})
	plugin := filepath.Join(vault, ".obsidian", "plugins", "demo")
	livePlugin(t, plugin, "1.0.0")
	prior := obsidian.PluginState{Present: true, ID: "demo", Version: "1.0.0", Enabled: true, Loaded: true}
	client := newFakeClient(prior)
	client.inspectErrAt = 3
	client.inspectErr = errors.New("runtime state unavailable")
	client.onReload = func() {
		client.state = stateFromDisk(t, plugin, containsLive(enabledLive(t, vault), "demo"))
	}

	outcome, err := change.New().ApplyLive(context.Background(), change.PreparedChange{
		VaultRoot: vault, PluginID: "demo", Kind: change.Uninstall, Enabled: change.Disable,
	}, obsidian.NewCoordinator(client).Session(obsidian.ChangePlan{
		PluginID: "demo", PlannedState: prior, TargetAbsent: true,
	}))
	if err == nil {
		t.Fatal("expected runtime verification failure")
	}
	if outcome.Changed || !outcome.Restored {
		t.Fatalf("outcome = %+v", outcome)
	}
	if got := stateFromDisk(t, plugin, true); got != prior {
		t.Fatalf("restored disk state = %+v, want %+v", got, prior)
	}
	if client.state != prior {
		t.Fatalf("restored runtime state = %+v, want %+v", client.state, prior)
	}
	wantCalls := []string{"probe", "inspect", "inspect", "disable", "inspect", "reload", "enable", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
	assertNoLiveRecovery(t, vault)
}

func liveVault(t *testing.T, enabled []string) string {
	t.Helper()
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, ".obsidian", "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeLiveJSON(t, filepath.Join(vault, ".obsidian", "community-plugins.json"), enabled)
	return vault
}

func livePlugin(t *testing.T, dir, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeLiveJSON(t, filepath.Join(dir, "manifest.json"), map[string]string{"id": "demo", "version": version})
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte("plugin"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func stateFromDisk(t *testing.T, plugin string, enabled bool) obsidian.PluginState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(plugin, "manifest.json"))
	if os.IsNotExist(err) {
		return obsidian.PluginState{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return obsidian.PluginState{Present: true, ID: manifest.ID, Version: manifest.Version, Enabled: enabled, Loaded: enabled}
}

func enabledLive(t *testing.T, vault string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(vault, ".obsidian", "community-plugins.json"))
	if err != nil {
		t.Fatal(err)
	}
	var enabled []string
	if err := json.Unmarshal(data, &enabled); err != nil {
		t.Fatal(err)
	}
	return enabled
}

func containsLive(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func writeLiveJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertNoLiveRecovery(t *testing.T, vault string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(vault, ".obsidian", ".plugman", "recovery", "current")); !os.IsNotExist(err) {
		t.Fatalf("recovery material remains: %v", err)
	}
}
