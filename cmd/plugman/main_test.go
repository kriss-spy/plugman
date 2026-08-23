package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListJSONUsesManagerReportSchema(t *testing.T) {
	vaultRoot := t.TempDir()
	pluginRoot := filepath.Join(vaultRoot, ".obsidian", "plugins", "dataview")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "manifest.json"), []byte(`{"id":"dataview","version":"0.5.67"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"list", "--json"}, vaultRoot, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), "\x1b") {
		t.Errorf("JSON contains ANSI escape: %q", stdout.String())
	}
	var output struct {
		SchemaVersion int `json:"schemaVersion"`
		Plugins       []struct {
			ID string `json:"id"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
	}
	if output.SchemaVersion != 1 || len(output.Plugins) != 1 || output.Plugins[0].ID != "dataview" {
		t.Errorf("output = %#v", output)
	}
}
