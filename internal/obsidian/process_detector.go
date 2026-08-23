package obsidian

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ProcessResult separates a normal "no matching process" exit code from a
// failure to perform process detection at all.
type ProcessResult struct {
	Output   []byte
	ExitCode int
}

// ProcessRunner invokes a platform process-listing utility without a shell.
type ProcessRunner interface {
	RunProcess(context.Context, string, ...string) (ProcessResult, error)
}

type processRuntimeDetector struct {
	platform string
	runner   ProcessRunner
}

// NewProcessRuntimeDetector creates the default non-launching desktop process
// detector for the current platform.
func NewProcessRuntimeDetector(runner ProcessRunner) RuntimeDetector {
	return &processRuntimeDetector{platform: runtime.GOOS, runner: runner}
}

func (d *processRuntimeDetector) Detect(ctx context.Context, _ string) (RuntimeStatus, error) {
	if d.runner == nil {
		return RuntimeStatus{}, errors.New("process runner is required")
	}
	var (
		result ProcessResult
		err    error
	)
	switch d.platform {
	case "linux", "darwin":
		result, err = d.runner.RunProcess(ctx, "pgrep", "-x", "-i", "obsidian")
	case "windows":
		result, err = d.runner.RunProcess(ctx, "tasklist", "/FI", "IMAGENAME eq Obsidian.exe", "/FO", "CSV", "/NH")
	default:
		return RuntimeStatus{}, fmt.Errorf("unsupported desktop platform %q", d.platform)
	}
	if err != nil {
		return RuntimeStatus{}, fmt.Errorf("inspect Obsidian process: %w", err)
	}

	if d.platform == "windows" {
		if result.ExitCode != 0 {
			return RuntimeStatus{}, fmt.Errorf("tasklist exited with status %d", result.ExitCode)
		}
		running := strings.Contains(strings.ToLower(string(result.Output)), "obsidian.exe")
		return runtimeStatus(running), nil
	}
	if result.ExitCode == 1 {
		return runtimeStatus(false), nil
	}
	if result.ExitCode != 0 {
		return RuntimeStatus{}, fmt.Errorf("pgrep exited with status %d", result.ExitCode)
	}
	if strings.TrimSpace(string(result.Output)) == "" {
		return RuntimeStatus{}, errors.New("pgrep reported success without a process ID")
	}
	return runtimeStatus(true), nil
}

func runtimeStatus(running bool) RuntimeStatus {
	return RuntimeStatus{Running: running, CLISupported: running, SafeToInvoke: running}
}

// ExecProcessRunner directly invokes the platform process-listing utility.
type ExecProcessRunner struct{}

func (ExecProcessRunner) RunProcess(ctx context.Context, executable string, args ...string) (ProcessResult, error) {
	output, err := exec.CommandContext(ctx, executable, args...).CombinedOutput()
	if err == nil {
		return ProcessResult{Output: output}, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return ProcessResult{Output: output, ExitCode: exitErr.ExitCode()}, nil
	}
	return ProcessResult{}, err
}

var _ RuntimeDetector = (*processRuntimeDetector)(nil)
var _ ProcessRunner = ExecProcessRunner{}
