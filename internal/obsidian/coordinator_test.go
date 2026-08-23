package obsidian_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/kriss-spy/plugman/internal/obsidian"
)

func TestCoordinatorSafelyReplacesLoadedPlugin(t *testing.T) {
	client := newFakeClient(obsidian.PluginState{
		Present: true, ID: "dataview", Version: "1.0.0", Enabled: true, Loaded: true,
	})
	replaced := false
	client.onReload = func() {
		client.state = obsidian.PluginState{Present: true, ID: "dataview", Version: "2.0.0"}
	}

	result, err := obsidian.NewCoordinator(client).Replace(context.Background(), obsidian.ReplaceRequest{
		PluginID:      "dataview",
		PlannedState:  client.state,
		TargetVersion: "2.0.0",
		ReplaceFiles:  func(context.Context) error { replaced = true; return nil },
		RestoreFiles:  func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if !replaced {
		t.Fatal("replacement callback was not invoked")
	}
	wantCalls := []string{"probe", "inspect", "inspect", "disable", "reload", "enable", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
	if result.State.Version != "2.0.0" || !result.State.Enabled || !result.State.Loaded {
		t.Fatalf("result state = %+v", result.State)
	}
	if result.Restore.Attempted {
		t.Fatalf("unexpected restore outcome: %+v", result.Restore)
	}
}

func TestCoordinatorRestoresFilesAndRuntimeWhenReloadFails(t *testing.T) {
	prior := obsidian.PluginState{Present: true, ID: "dataview", Version: "1.0.0", Enabled: true, Loaded: true}
	client := newFakeClient(prior)
	client.failOnce["reload"] = errors.New("runtime rejected reload")
	restored := false
	client.onReload = func() { client.state = prior }

	result, err := obsidian.NewCoordinator(client).Replace(context.Background(), obsidian.ReplaceRequest{
		PluginID: "dataview", PlannedState: prior, TargetVersion: "2.0.0",
		ReplaceFiles: func(context.Context) error {
			client.state.Version = "2.0.0"
			return nil
		},
		RestoreFiles: func(context.Context) error { restored = true; return nil },
	})
	if err == nil {
		t.Fatal("expected reload failure")
	}
	if !restored || !result.Restore.Attempted || !result.Restore.FilesRestored || !result.Restore.RuntimeRestored {
		t.Fatalf("restore outcome = %+v, callback called = %v", result.Restore, restored)
	}
	wantCalls := []string{"probe", "inspect", "inspect", "disable", "reload", "reload", "enable", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
}

func TestCoordinatorDoesNotReplaceWhenObsidianExitsDuringRecheck(t *testing.T) {
	prior := obsidian.PluginState{Present: true, ID: "dataview", Version: "1.0.0", Enabled: true, Loaded: true}
	client := newFakeClient(prior)
	client.inspectErrAt = 2
	client.inspectErr = errors.New("Obsidian exited")
	replaced := false

	_, err := obsidian.NewCoordinator(client).Replace(context.Background(), obsidian.ReplaceRequest{
		PluginID: "dataview", PlannedState: prior, TargetVersion: "2.0.0",
		ReplaceFiles: func(context.Context) error { replaced = true; return nil },
		RestoreFiles: func(context.Context) error { return nil },
	})
	if err == nil || replaced {
		t.Fatalf("err = %v, replaced = %v", err, replaced)
	}
	wantCalls := []string{"probe", "inspect", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
}

func TestCoordinatorPreservesDisabledPluginState(t *testing.T) {
	prior := obsidian.PluginState{Present: true, ID: "dataview", Version: "1.0.0"}
	client := newFakeClient(prior)
	client.onReload = func() {
		client.state = obsidian.PluginState{Present: true, ID: "dataview", Version: "2.0.0"}
	}

	result, err := obsidian.NewCoordinator(client).Replace(context.Background(), obsidian.ReplaceRequest{
		PluginID: "dataview", PlannedState: prior, TargetVersion: "2.0.0",
		ReplaceFiles: func(context.Context) error { return nil },
		RestoreFiles: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if result.State.Enabled || result.State.Loaded {
		t.Fatalf("disabled plugin was activated: %+v", result.State)
	}
	wantCalls := []string{"probe", "inspect", "inspect", "reload", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
}

func TestCoordinatorInstallsNewPluginDisabledByDefault(t *testing.T) {
	client := newFakeClient(obsidian.PluginState{})
	client.onReload = func() {
		client.state = obsidian.PluginState{Present: true, ID: "dataview", Version: "2.0.0"}
	}

	result, err := obsidian.NewCoordinator(client).Replace(context.Background(), obsidian.ReplaceRequest{
		PluginID: "dataview", PlannedState: obsidian.PluginState{}, TargetVersion: "2.0.0",
		ReplaceFiles: func(context.Context) error { return nil },
		RestoreFiles: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if result.State.Enabled || result.State.Loaded {
		t.Fatalf("new plugin was activated: %+v", result.State)
	}
	wantCalls := []string{"probe", "inspect", "inspect", "reload", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
}

func TestCoordinatorRestoresAbsentStateWithoutReloadingRemovedPlugin(t *testing.T) {
	client := newFakeClient(obsidian.PluginState{})
	client.failOnce["reload"] = errors.New("reload failed")
	client.onReload = func() {
		client.state = obsidian.PluginState{Present: true, ID: "dataview", Version: "2.0.0"}
	}

	result, err := obsidian.NewCoordinator(client).Replace(context.Background(), obsidian.ReplaceRequest{
		PluginID: "dataview", PlannedState: obsidian.PluginState{}, TargetVersion: "2.0.0",
		ReplaceFiles: func(context.Context) error {
			client.state = obsidian.PluginState{Present: true, ID: "dataview", Version: "2.0.0"}
			return nil
		},
		RestoreFiles: func(context.Context) error { client.state = obsidian.PluginState{}; return nil },
	})
	if err == nil {
		t.Fatal("expected reload failure")
	}
	if !result.Restore.RuntimeRestored || result.Restore.Err != nil {
		t.Fatalf("restore outcome = %+v", result.Restore)
	}
	wantCalls := []string{"probe", "inspect", "inspect", "reload", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
}

func TestCoordinatorRejectsConcurrentStateChangeBeforeMutation(t *testing.T) {
	prior := obsidian.PluginState{Present: true, ID: "dataview", Version: "1.0.0", Enabled: true, Loaded: true}
	client := newFakeClient(prior)
	client.afterInspect = func(call int) {
		if call == 1 {
			client.state.Enabled = false
			client.state.Loaded = false
		}
	}
	replaced := false

	_, err := obsidian.NewCoordinator(client).Replace(context.Background(), obsidian.ReplaceRequest{
		PluginID: "dataview", PlannedState: prior, TargetVersion: "2.0.0",
		ReplaceFiles: func(context.Context) error { replaced = true; return nil },
		RestoreFiles: func(context.Context) error { return nil },
	})
	if !errors.Is(err, obsidian.ErrStateChanged) || replaced {
		t.Fatalf("err = %v, replaced = %v", err, replaced)
	}
	wantCalls := []string{"probe", "inspect", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
}

func TestUninstallCommitAcceptsQuiescedCachedManifest(t *testing.T) {
	prior := obsidian.PluginState{Present: true, ID: "dataview", Version: "1.0.0", Enabled: true, Loaded: true}
	client := newFakeClient(prior)
	session := obsidian.NewCoordinator(client).Session(obsidian.ChangePlan{
		PluginID: "dataview", PlannedState: prior, TargetAbsent: true,
	})
	if err := session.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := session.Commit(context.Background()); err != nil {
		t.Fatalf("Commit() rejected quiesced cached manifest: %v", err)
	}
	wantCalls := []string{"probe", "inspect", "inspect", "disable", "inspect"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", client.calls, wantCalls)
	}
}

type fakeClient struct {
	state         obsidian.PluginState
	calls         []string
	onReload      func()
	fail          map[string]error
	failOnce      map[string]error
	inspected     int
	inspectErrAt  int
	inspectErr    error
	beforeInspect func(int)
	afterInspect  func(int)
}

func newFakeClient(state obsidian.PluginState) *fakeClient {
	return &fakeClient{state: state, fail: make(map[string]error), failOnce: make(map[string]error)}
}

func (f *fakeClient) Probe(context.Context) error {
	f.calls = append(f.calls, "probe")
	return f.fail["probe"]
}

func (f *fakeClient) Inspect(context.Context, string) (obsidian.PluginState, error) {
	f.calls = append(f.calls, "inspect")
	f.inspected++
	if f.beforeInspect != nil {
		f.beforeInspect(f.inspected)
	}
	if f.inspected == f.inspectErrAt {
		return obsidian.PluginState{}, f.inspectErr
	}
	state := f.state
	if f.afterInspect != nil {
		f.afterInspect(f.inspected)
	}
	return state, f.fail["inspect"]
}

func (f *fakeClient) Disable(context.Context, string) error {
	f.calls = append(f.calls, "disable")
	if err := f.fail["disable"]; err != nil {
		return err
	}
	f.state.Enabled = false
	f.state.Loaded = false
	return nil
}

func (f *fakeClient) Unload(context.Context, string) error {
	f.calls = append(f.calls, "unload")
	if err := f.fail["unload"]; err != nil {
		return err
	}
	f.state.Loaded = false
	return nil
}

func (f *fakeClient) Reload(context.Context, string) error {
	f.calls = append(f.calls, "reload")
	if err := f.failOnce["reload"]; err != nil {
		delete(f.failOnce, "reload")
		return err
	}
	if err := f.fail["reload"]; err != nil {
		return err
	}
	if f.onReload != nil {
		f.onReload()
	}
	return nil
}

func (f *fakeClient) Enable(context.Context, string) error {
	f.calls = append(f.calls, "enable")
	if err := f.fail["enable"]; err != nil {
		return err
	}
	f.state.Enabled = true
	f.state.Loaded = true
	return nil
}

var _ obsidian.Client = (*fakeClient)(nil)
var _ = errors.New
