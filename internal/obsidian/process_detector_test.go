package obsidian

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestProcessRuntimeDetectorFindsLinuxObsidianWithoutShell(t *testing.T) {
	runner := &fakeProcessRunner{result: ProcessResult{Output: []byte("4217\n")}}
	detector := &processRuntimeDetector{platform: "linux", runner: runner}

	status, err := detector.Detect(context.Background(), "/vault")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !status.Running || !status.CLISupported || !status.SafeToInvoke {
		t.Fatalf("status = %+v", status)
	}
	want := processInvocation{name: "pgrep", args: []string{"-x", "-i", "obsidian"}}
	if !reflect.DeepEqual(runner.invocation, want) {
		t.Fatalf("invocation = %+v, want %+v", runner.invocation, want)
	}
}

func TestProcessRuntimeDetectorConfirmsLinuxObsidianStopped(t *testing.T) {
	runner := &fakeProcessRunner{result: ProcessResult{ExitCode: 1}}
	detector := &processRuntimeDetector{platform: "linux", runner: runner}

	status, err := detector.Detect(context.Background(), "/vault")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status.Running {
		t.Fatalf("status = %+v", status)
	}
}

func TestProcessRuntimeDetectorFindsWindowsObsidian(t *testing.T) {
	runner := &fakeProcessRunner{result: ProcessResult{Output: []byte(`"Obsidian.exe","992","Console"`)}}
	detector := &processRuntimeDetector{platform: "windows", runner: runner}

	status, err := detector.Detect(context.Background(), `C:\Vault`)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !status.Running {
		t.Fatalf("status = %+v", status)
	}
	want := processInvocation{name: "tasklist", args: []string{"/FI", "IMAGENAME eq Obsidian.exe", "/FO", "CSV", "/NH"}}
	if !reflect.DeepEqual(runner.invocation, want) {
		t.Fatalf("invocation = %+v, want %+v", runner.invocation, want)
	}
}

func TestProcessRuntimeDetectorReturnsUncertainErrors(t *testing.T) {
	tests := []struct {
		name     string
		detector *processRuntimeDetector
	}{
		{"utility unavailable", &processRuntimeDetector{platform: "linux", runner: &fakeProcessRunner{err: errors.New("not found")}}},
		{"unexpected status", &processRuntimeDetector{platform: "linux", runner: &fakeProcessRunner{result: ProcessResult{ExitCode: 2}}}},
		{"unknown platform", &processRuntimeDetector{platform: "plan9", runner: &fakeProcessRunner{}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.detector.Detect(context.Background(), "/vault"); err == nil {
				t.Fatal("expected uncertain detection error")
			}
		})
	}
}

type processInvocation struct {
	name string
	args []string
}

type fakeProcessRunner struct {
	result     ProcessResult
	err        error
	invocation processInvocation
}

func (r *fakeProcessRunner) RunProcess(_ context.Context, name string, args ...string) (ProcessResult, error) {
	r.invocation = processInvocation{name: name, args: append([]string(nil), args...)}
	return r.result, r.err
}

var _ ProcessRunner = (*fakeProcessRunner)(nil)
