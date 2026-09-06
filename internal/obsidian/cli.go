package obsidian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	// ErrCLIUnavailable means the Obsidian executable is not registered.
	ErrCLIUnavailable = errors.New("Obsidian CLI unavailable")
	// ErrCLIUnsupported means safe live control cannot be established.
	ErrCLIUnsupported = errors.New("Obsidian CLI unsupported")
	// ErrObsidianNotRunning means a non-launching check confirmed the app is stopped.
	ErrObsidianNotRunning = errors.New("Obsidian is not running")
	// ErrObsidianOtherVault means Obsidian is running but focused on a Vault
	// other than the exact target, so live coordination does not apply to the
	// target Vault.
	ErrObsidianOtherVault = errors.New("Obsidian is open on a different Vault")
)

// Command describes one direct executable invocation. Arguments are passed
// without a shell, and Dir is the exact Vault Root used for CLI selection.
type Command struct {
	Executable string
	Args       []string
	Dir        string
}

// CommandRunner invokes Obsidian directly and reports whether its executable
// is registered. Tests and embedders may supply a deterministic implementation.
type CommandRunner interface {
	Available(string) bool
	Run(context.Context, Command) ([]byte, error)
}

// RuntimeStatus must be discovered without invoking the Obsidian CLI, because
// the CLI launches Obsidian when the application is stopped.
type RuntimeStatus struct {
	Running      bool
	CLISupported bool
	// SafeToInvoke means the detector positively identified a running desktop
	// process immediately before CLI invocation. Detectors must leave this
	// false when process state is uncertain.
	SafeToInvoke bool
}

// RuntimeDetector performs a non-launching runtime check for a Vault.
type RuntimeDetector interface {
	Detect(context.Context, string) (RuntimeStatus, error)
}

// CLIConfig identifies the CLI executable and exact target Vault Root.
type CLIConfig struct {
	Executable string
	VaultPath  string
	Runner     CommandRunner
	Detector   RuntimeDetector
}

// CLIClient maps the live coordination seam to the official Obsidian CLI.
type CLIClient struct {
	config CLIConfig
}

func NewCLIClient(config CLIConfig) *CLIClient {
	if config.Executable == "" {
		config.Executable = "obsidian"
	}
	if config.Runner == nil {
		config.Runner = ExecCommandRunner{}
	}
	if config.Detector == nil {
		config.Detector = NewProcessRuntimeDetector(ExecProcessRunner{})
	}
	return &CLIClient{config: config}
}

// Probe verifies the already-running CLI selected the exact Vault and exposes
// the private manifest-refresh capability required before live mutation.
func (c *CLIClient) Probe(ctx context.Context) error {
	output, err := c.refreshManifests(ctx)
	if err != nil {
		for _, runtimeErr := range []error{ErrObsidianNotRunning, ErrObsidianOtherVault, ErrCLIUnavailable, ErrCLIUnsupported} {
			if errors.Is(err, runtimeErr) {
				return err
			}
		}
		return fmt.Errorf("%w: probe plugin manifest refresh: %v", ErrCLIUnsupported, err)
	}
	if err := requireEvalCompletion(output); err != nil {
		return fmt.Errorf("%w: plugin manifest refresh did not complete", ErrCLIUnsupported)
	}
	return nil
}

func (c *CLIClient) checkRuntime(ctx context.Context) error {
	if c.config.Detector == nil {
		return fmt.Errorf("%w: no non-launching runtime detector", ErrCLIUnsupported)
	}
	status, err := c.config.Detector.Detect(ctx, c.config.VaultPath)
	if err != nil {
		return fmt.Errorf("%w: detect running Obsidian: %v", ErrCLIUnsupported, err)
	}
	if !status.Running {
		return ErrObsidianNotRunning
	}
	if c.config.Runner == nil || !c.config.Runner.Available(c.config.Executable) {
		return ErrCLIUnavailable
	}
	if !status.CLISupported {
		return ErrCLIUnsupported
	}
	if !status.SafeToInvoke {
		return fmt.Errorf("%w: CLI invocation may launch Obsidian", ErrCLIUnsupported)
	}
	output, err := c.config.Runner.Run(ctx, Command{
		Executable: c.config.Executable,
		Args:       []string{"vault", "info=path"},
		Dir:        c.config.VaultPath,
	})
	if err != nil {
		return fmt.Errorf("%w: verify selected Vault: %v", ErrCLIUnsupported, err)
	}
	same, err := sameVaultRoot(c.config.VaultPath, strings.TrimSpace(string(output)))
	if err != nil {
		return fmt.Errorf("%w: verify selected Vault: %v", ErrCLIUnsupported, err)
	}
	if !same {
		return fmt.Errorf("%w: CLI selected a different Vault than the exact target Vault Root", ErrObsidianOtherVault)
	}
	return nil
}

func sameVaultRoot(target, selected string) (bool, error) {
	if selected == "" || !filepath.IsAbs(selected) {
		return false, errors.New("Obsidian CLI returned a non-absolute Vault path")
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return false, err
	}
	target = filepath.Clean(target)
	selected = filepath.Clean(selected)
	if targetInfo, targetErr := os.Stat(target); targetErr == nil {
		if selectedInfo, selectedErr := os.Stat(selected); selectedErr == nil && os.SameFile(targetInfo, selectedInfo) {
			return true, nil
		}
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(target, selected), nil
	}
	return target == selected, nil
}

func (c *CLIClient) Inspect(ctx context.Context, pluginID string) (PluginState, error) {
	id, err := json.Marshal(pluginID)
	if err != nil {
		return PluginState{}, fmt.Errorf("encode plugin ID: %w", err)
	}
	code := `(function(){const id=` + string(id) + `;const p=app.plugins;const m=p&&p.manifests&&p.manifests[id];return JSON.stringify(m?{present:true,id:m.id,version:m.version,enabled:p.enabledPlugins.has(id),loaded:!!p.plugins[id]}:{present:false,id:"",version:"",enabled:false,loaded:false})})()`
	output, err := c.run(ctx, "eval", "code="+code)
	if err != nil {
		return PluginState{}, fmt.Errorf("inspect plugin %q: %w", pluginID, err)
	}
	var state PluginState
	if err := decodeEvalJSON(output, &state); err != nil {
		return PluginState{}, fmt.Errorf("decode plugin state: %w", err)
	}
	return state, nil
}

// decodeEvalJSON strips the Obsidian CLI's return-value marker before decoding
// the JSON value an eval script emitted. CLI versions use one or more equals
// signs before the closing angle bracket.
func decodeEvalJSON(output string, destination any) error {
	value := strings.TrimSpace(output)
	afterEquals := strings.TrimLeft(value, "=")
	if len(afterEquals) != len(value) && strings.HasPrefix(afterEquals, ">") {
		value = strings.TrimSpace(afterEquals[1:])
	}
	return json.Unmarshal([]byte(value), destination)
}

func requireEvalCompletion(output string) error {
	var completed bool
	if err := decodeEvalJSON(output, &completed); err != nil {
		return err
	}
	if !completed {
		return errors.New("operation returned false")
	}
	return nil
}

func (c *CLIClient) Disable(ctx context.Context, pluginID string) error {
	_, err := c.run(ctx, "plugin:disable", "id="+pluginID, "filter=community")
	return err
}

func (c *CLIClient) Unload(ctx context.Context, pluginID string) error {
	id, err := json.Marshal(pluginID)
	if err != nil {
		return fmt.Errorf("encode plugin ID: %w", err)
	}
	output, err := c.run(ctx, "eval", "code=(async()=>{await app.plugins.unloadPlugin("+string(id)+");return true})()")
	if err != nil {
		return err
	}
	if err := requireEvalCompletion(output); err != nil {
		return fmt.Errorf("unload plugin %q did not complete: %w", pluginID, err)
	}
	return nil
}

func (c *CLIClient) Refresh(ctx context.Context) error {
	output, err := c.refreshManifests(ctx)
	if err != nil {
		return err
	}
	if err := requireEvalCompletion(output); err != nil {
		return fmt.Errorf("plugin manifest refresh did not complete: %w", err)
	}
	return nil
}

func (c *CLIClient) refreshManifests(ctx context.Context) (string, error) {
	return c.run(ctx, "eval", "code=(async()=>{await app.plugins.loadManifests();return true})()")
}

func (c *CLIClient) Reload(ctx context.Context, pluginID string) error {
	_, err := c.run(ctx, "plugin:reload", "id="+pluginID)
	return err
}

func (c *CLIClient) Enable(ctx context.Context, pluginID string) error {
	_, err := c.run(ctx, "plugin:enable", "id="+pluginID, "filter=community")
	return err
}

func (c *CLIClient) run(ctx context.Context, args ...string) (string, error) {
	// Recheck before every command so an Obsidian exit between coordinator
	// steps cannot turn the next CLI call into an application launch.
	if err := c.checkRuntime(ctx); err != nil {
		return "", err
	}
	output, err := c.config.Runner.Run(ctx, Command{
		Executable: c.config.Executable,
		Args:       append([]string(nil), args...),
		Dir:        c.config.VaultPath,
	})
	if err != nil {
		return string(output), fmt.Errorf("run Obsidian CLI: %w", err)
	}
	return string(output), nil
}

// ExecCommandRunner invokes the configured executable directly through
// os/exec. It never uses a command shell.
type ExecCommandRunner struct{}

func (ExecCommandRunner) Available(executable string) bool {
	_, err := exec.LookPath(executable)
	return err == nil
}

func (ExecCommandRunner) Run(ctx context.Context, command Command) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command.Executable, command.Args...)
	cmd.Dir = command.Dir
	return cmd.CombinedOutput()
}

var _ Client = (*CLIClient)(nil)
var _ CommandRunner = ExecCommandRunner{}
