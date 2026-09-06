package obsidian_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kriss-spy/plugman/internal/obsidian"
)

func TestCLIClientProbeDoesNotInvokeCLIWhenObsidianIsStopped(t *testing.T) {
	// A confirmed-stopped Vault can safely use the closed adapter even when no
	// Obsidian CLI executable is registered.
	runner := &fakeCommandRunner{available: false}
	detector := staticDetector{status: obsidian.RuntimeStatus{Running: false, CLISupported: true}}
	client := obsidian.NewCLIClient(obsidian.CLIConfig{
		Executable: "obsidian", VaultPath: "/vault", Runner: runner, Detector: detector,
	})

	err := client.Probe(context.Background())
	if !errors.Is(err, obsidian.ErrObsidianNotRunning) {
		t.Fatalf("Probe error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("probe invoked CLI and could launch Obsidian: %v", runner.commands)
	}
}

func TestCLIClientRefusesUncertainTargetWhenAnotherVaultIsSelected(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target", "Notes")
	other := filepath.Join(root, "other", "Notes")
	for _, path := range []string{target, other} {
		if err := os.MkdirAll(filepath.Join(path, ".obsidian"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runner := &fakeCommandRunner{available: true, selectedVaultPath: other}
	client := obsidian.NewCLIClient(obsidian.CLIConfig{
		VaultPath: target, Runner: runner,
		Detector: staticDetector{status: obsidian.RuntimeStatus{Running: true, CLISupported: true, SafeToInvoke: true}},
	})

	err := client.Probe(context.Background())
	if !errors.Is(err, obsidian.ErrObsidianOtherVault) {
		t.Fatalf("Probe error = %v, want other-Vault refusal", err)
	}
	if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].Args, []string{"vault", "info=path"}) {
		t.Fatalf("commands = %+v; only read-only Vault identity verification is allowed", runner.commands)
	}
}

func TestCLIClientTargetsExactVaultWhenBasenamesMatch(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target", "Notes")
	other := filepath.Join(root, "other", "Notes")
	for _, path := range []string{target, other} {
		if err := os.MkdirAll(filepath.Join(path, ".obsidian"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runner := &fakeCommandRunner{
		available: true, selectedVaultPath: target,
		outputs: []string{`{"present":false,"id":"","version":"","enabled":false,"loaded":false}`},
	}
	client := obsidian.NewCLIClient(obsidian.CLIConfig{
		VaultPath: target, Runner: runner,
		Detector: staticDetector{status: obsidian.RuntimeStatus{Running: true, CLISupported: true, SafeToInvoke: true}},
	})

	if _, err := client.Inspect(context.Background(), "sample"); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %+v", runner.commands)
	}
	for _, command := range runner.commands {
		if command.Dir != target {
			t.Fatalf("command Dir = %q, want exact target %q", command.Dir, target)
		}
		for _, arg := range command.Args {
			if strings.HasPrefix(arg, "vault=") {
				t.Fatalf("basename selector leaked into command: %v", command.Args)
			}
		}
	}
}

func TestCLIClientProbeDistinguishesUnavailableAndUnsupported(t *testing.T) {
	tests := []struct {
		name      string
		runner    *fakeCommandRunner
		detector  obsidian.RuntimeDetector
		wantError error
	}{
		{
			name: "CLI executable unavailable", runner: &fakeCommandRunner{},
			detector:  staticDetector{status: obsidian.RuntimeStatus{Running: true, CLISupported: true, SafeToInvoke: true}},
			wantError: obsidian.ErrCLIUnavailable,
		},
		{
			name: "runtime CLI unsupported", runner: &fakeCommandRunner{available: true},
			detector:  staticDetector{status: obsidian.RuntimeStatus{Running: true}},
			wantError: obsidian.ErrCLIUnsupported,
		},
		{
			name: "CLI may launch app", runner: &fakeCommandRunner{available: true},
			detector:  staticDetector{status: obsidian.RuntimeStatus{Running: true, CLISupported: true}},
			wantError: obsidian.ErrCLIUnsupported,
		},
		{
			name: "runtime status unknown", runner: &fakeCommandRunner{available: true},
			detector:  staticDetector{err: errors.New("cannot inspect process")},
			wantError: obsidian.ErrCLIUnsupported,
		},
		{
			name: "detector unavailable", runner: &fakeCommandRunner{available: true},
			detector:  staticDetector{err: errors.New("detector unavailable")},
			wantError: obsidian.ErrCLIUnsupported,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := obsidian.NewCLIClient(obsidian.CLIConfig{
				VaultPath: "/vault", Runner: test.runner, Detector: test.detector,
			})
			if err := client.Probe(context.Background()); !errors.Is(err, test.wantError) {
				t.Fatalf("Probe error = %v, want %v", err, test.wantError)
			}
			if len(test.runner.commands) != 0 {
				t.Fatalf("probe invoked CLI: %v", test.runner.commands)
			}
		})
	}
}

func TestCLIClientProbeRejectsMissingManifestRefreshBeforeMutation(t *testing.T) {
	runner := &fakeCommandRunner{available: true, errors: []error{errors.New("loadManifests is unavailable")}}
	client := obsidian.NewCLIClient(obsidian.CLIConfig{
		VaultPath: "/vault", Runner: runner,
		Detector: staticDetector{status: obsidian.RuntimeStatus{Running: true, CLISupported: true, SafeToInvoke: true}},
	})

	err := client.Probe(context.Background())
	if !errors.Is(err, obsidian.ErrCLIUnsupported) {
		t.Fatalf("Probe error = %v, want unsupported runtime", err)
	}
	wantArgs := [][]string{{"vault", "info=path"}, {"eval", "code=await app.plugins.loadManifests();true"}}
	if len(runner.commands) != len(wantArgs) {
		t.Fatalf("commands = %+v", runner.commands)
	}
	for index, command := range runner.commands {
		if !reflect.DeepEqual(command.Args, wantArgs[index]) {
			t.Errorf("command %d args = %v, want %v", index, command.Args, wantArgs[index])
		}
	}
}

func TestCLIClientInspectsPluginThroughTargetedVault(t *testing.T) {
	vault := t.TempDir()
	runner := &fakeCommandRunner{
		available: true,
		outputs:   []string{`{"present":true,"id":"sample","version":"1.2.3","enabled":true,"loaded":true}`},
	}
	client := obsidian.NewCLIClient(obsidian.CLIConfig{
		Executable: "obsidian", VaultPath: vault, Runner: runner,
		Detector: staticDetector{status: obsidian.RuntimeStatus{Running: true, CLISupported: true, SafeToInvoke: true}},
	})

	state, err := client.Inspect(context.Background(), `sample`)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	wantState := obsidian.PluginState{Present: true, ID: "sample", Version: "1.2.3", Enabled: true, Loaded: true}
	if state != wantState {
		t.Fatalf("state = %+v, want %+v", state, wantState)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %v", runner.commands)
	}
	if !reflect.DeepEqual(runner.commands[0].Args, []string{"vault", "info=path"}) {
		t.Fatalf("Vault verification = %+v", runner.commands[0])
	}
	command := runner.commands[1]
	if command.Executable != "obsidian" || command.Dir != vault || len(command.Args) != 2 {
		t.Fatalf("command = %+v", command)
	}
	if command.Args[0] != "eval" || !strings.HasPrefix(command.Args[1], "code=") {
		t.Fatalf("arguments = %v", command.Args)
	}
}

func TestCLIClientInspectStripsCLIReturnValuePrefix(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "standard", output: `=> {"present":true,"id":"sample","version":"1.2.3","enabled":false,"loaded":false}`},
		{name: "verbose", output: `==> {"present":true,"id":"sample","version":"1.2.3","enabled":false,"loaded":false}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vault := t.TempDir()
			runner := &fakeCommandRunner{available: true, outputs: []string{test.output}}
			client := obsidian.NewCLIClient(obsidian.CLIConfig{
				VaultPath: vault, Runner: runner,
				Detector: staticDetector{status: obsidian.RuntimeStatus{Running: true, CLISupported: true, SafeToInvoke: true}},
			})

			state, err := client.Inspect(context.Background(), "sample")
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			want := obsidian.PluginState{Present: true, ID: "sample", Version: "1.2.3"}
			if state != want {
				t.Fatalf("state = %+v, want %+v", state, want)
			}
		})
	}
}

func TestCLIClientReportsRuntimeStateWithoutReadingPluginFiles(t *testing.T) {
	vault := filepath.Join(t.TempDir(), "missing-vault")
	runner := &fakeCommandRunner{
		available: true,
		outputs:   []string{`{"present":true,"id":"sample","version":"1.2.3","enabled":false,"loaded":false}`},
	}
	client := obsidian.NewCLIClient(obsidian.CLIConfig{
		VaultPath: vault, Runner: runner,
		Detector: staticDetector{status: obsidian.RuntimeStatus{Running: true, CLISupported: true, SafeToInvoke: true}},
	})

	state, err := client.Inspect(context.Background(), "sample")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	want := obsidian.PluginState{Present: true, ID: "sample", Version: "1.2.3"}
	if state != want {
		t.Fatalf("state = %+v, want cached runtime manifest %+v", state, want)
	}
}

func TestCLIClientMapsPluginLifecycleCommandsWithoutShell(t *testing.T) {
	vault := t.TempDir()
	runner := &fakeCommandRunner{available: true}
	client := obsidian.NewCLIClient(obsidian.CLIConfig{
		Executable: "obsidian-custom", VaultPath: vault, Runner: runner,
		Detector: staticDetector{status: obsidian.RuntimeStatus{Running: true, CLISupported: true, SafeToInvoke: true}},
	})
	ctx := context.Background()

	if err := client.Disable(ctx, "sample"); err != nil {
		t.Fatal(err)
	}
	if err := client.Unload(ctx, "sample"); err != nil {
		t.Fatal(err)
	}
	if err := client.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Reload(ctx, "sample"); err != nil {
		t.Fatal(err)
	}
	if err := client.Enable(ctx, "sample"); err != nil {
		t.Fatal(err)
	}

	wantArgs := [][]string{
		{"vault", "info=path"}, {"plugin:disable", "id=sample", "filter=community"},
		{"vault", "info=path"}, {"eval", `code=await app.plugins.unloadPlugin("sample")`},
		{"vault", "info=path"}, {"eval", "code=await app.plugins.loadManifests();true"},
		{"vault", "info=path"}, {"plugin:reload", "id=sample"},
		{"vault", "info=path"}, {"plugin:enable", "id=sample", "filter=community"},
	}
	if len(runner.commands) != len(wantArgs) {
		t.Fatalf("commands = %+v", runner.commands)
	}
	for i, command := range runner.commands {
		if command.Executable != "obsidian-custom" || command.Dir != vault || !reflect.DeepEqual(command.Args, wantArgs[i]) {
			t.Errorf("command %d = %+v, want args %v", i, command, wantArgs[i])
		}
	}
}

func TestCLIClientRechecksRuntimeBeforeEveryCommand(t *testing.T) {
	vault := t.TempDir()
	runner := &fakeCommandRunner{available: true}
	runner.outputs = []string{"=> true"}
	detector := &sequenceDetector{statuses: []obsidian.RuntimeStatus{
		{Running: true, CLISupported: true, SafeToInvoke: true},
		{Running: false},
	}}
	client := obsidian.NewCLIClient(obsidian.CLIConfig{
		VaultPath: vault, Runner: runner, Detector: detector,
	})

	if err := client.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if err := client.Disable(context.Background(), "sample"); !errors.Is(err, obsidian.ErrObsidianNotRunning) {
		t.Fatalf("Disable error = %v", err)
	}
	if len(runner.commands) != 2 || !reflect.DeepEqual(runner.commands[0].Args, []string{"vault", "info=path"}) || runner.commands[1].Args[0] != "eval" {
		t.Fatalf("unexpected CLI invocation after Obsidian exited: %v", runner.commands)
	}
}

type staticDetector struct {
	status obsidian.RuntimeStatus
	err    error
}

type sequenceDetector struct {
	statuses []obsidian.RuntimeStatus
	calls    int
}

func (d *sequenceDetector) Detect(context.Context, string) (obsidian.RuntimeStatus, error) {
	index := d.calls
	d.calls++
	if index >= len(d.statuses) {
		index = len(d.statuses) - 1
	}
	return d.statuses[index], nil
}

func (d staticDetector) Detect(context.Context, string) (obsidian.RuntimeStatus, error) {
	return d.status, d.err
}

type fakeCommandRunner struct {
	available         bool
	selectedVaultPath string
	outputs           []string
	errors            []error
	commands          []obsidian.Command
	outputCalls       int
}

func (r *fakeCommandRunner) Available(string) bool { return r.available }

func (r *fakeCommandRunner) Run(_ context.Context, command obsidian.Command) ([]byte, error) {
	r.commands = append(r.commands, command)
	if reflect.DeepEqual(command.Args, []string{"vault", "info=path"}) {
		selected := r.selectedVaultPath
		if selected == "" {
			selected = command.Dir
		}
		return []byte(selected + "\n"), nil
	}
	index := r.outputCalls
	r.outputCalls++
	var output string
	if index < len(r.outputs) {
		output = r.outputs[index]
	}
	if index < len(r.errors) && r.errors[index] != nil {
		return []byte(output), r.errors[index]
	}
	return []byte(output), nil
}

var _ obsidian.CommandRunner = (*fakeCommandRunner)(nil)
var _ obsidian.RuntimeDetector = staticDetector{}
var _ obsidian.RuntimeDetector = (*sequenceDetector)(nil)
