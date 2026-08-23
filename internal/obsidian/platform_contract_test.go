package obsidian

import (
	"context"
	"reflect"
	"testing"
)

func TestPlatformRuntimeDetectorUsesDarwinObsidianProcessCheck(t *testing.T) {
	runner := &fakeProcessRunner{result: ProcessResult{Output: []byte("4217\n")}}
	detector := &processRuntimeDetector{platform: "darwin", runner: runner}

	status, err := detector.Detect(context.Background(), "/vault")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || !status.CLISupported || !status.SafeToInvoke {
		t.Fatalf("status = %+v", status)
	}
	want := processInvocation{name: "pgrep", args: []string{"-x", "-i", "obsidian"}}
	if !reflect.DeepEqual(runner.invocation, want) {
		t.Fatalf("invocation = %+v, want %+v", runner.invocation, want)
	}
}

func TestPlatformRuntimeDetectorFailsClosedOnUtilityErrors(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows"} {
		t.Run(platform, func(t *testing.T) {
			detector := &processRuntimeDetector{
				platform: platform,
				runner:   &fakeProcessRunner{result: ProcessResult{ExitCode: 2}},
			}
			if _, err := detector.Detect(context.Background(), "/vault"); err == nil {
				t.Fatalf("%s process utility failure was treated as safe runtime state", platform)
			}
		})
	}
}
