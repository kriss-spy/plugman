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

func TestBareUpdateAcceptsUpdateAllMode(t *testing.T) {
	vaultRoot := emptyVault(t)
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"update", "--dry-run"}, vaultRoot, &stdout, &stderr)

	if exitCode != 0 || !strings.Contains(stdout.String(), "PLUGIN") {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
}

func TestOutdatedJSONOnEmptyVault(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"outdated", "--json"}, emptyVault(t), &stdout, &stderr)
	if exitCode != 0 || !strings.Contains(stdout.String(), `"schemaVersion":1`) {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestOutdatedInteractiveShowsNetworkProgress(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runCommand([]string{"outdated"}, emptyVault(t), strings.NewReader(""), &stdout, &stderr, true)
	if exitCode != 0 || !strings.Contains(stderr.String(), "Checking plugin releases") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestUninstallNonInteractiveRequiresYes(t *testing.T) {
	vault := cliVaultWithPlugin(t, "demo", true)
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"uninstall", "demo"}, vault, &stdout, &stderr)
	if exitCode != 1 || !strings.Contains(stderr.String(), "requires --yes") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(vault, ".obsidian", "plugins", "demo")); err != nil {
		t.Fatalf("unconfirmed uninstall changed Vault: %v", err)
	}
}

func TestUninstallPromptsOnceAndCanCancel(t *testing.T) {
	vault := cliVaultWithPlugin(t, "demo", true)
	var stdout, stderr bytes.Buffer
	exitCode := runCommand([]string{"uninstall", "demo"}, vault, strings.NewReader("n\n"), &stdout, &stderr, true)
	if exitCode != 0 || strings.Count(stdout.String(), "Uninstall these plugins?") != 1 || !strings.Contains(stdout.String(), "demo") || !strings.Contains(stdout.String(), "Cancelled") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestUninstallYesRemovesPluginFolder(t *testing.T) {
	vault := cliVaultWithPlugin(t, "demo", false)
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"uninstall", "--yes", "demo"}, vault, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(vault, ".obsidian", "plugins", "demo")); !os.IsNotExist(err) {
		t.Fatalf("plugin folder remains: %v", err)
	}
}

func TestUninstallDryRunDoesNotRequireConfirmationOrChangeVault(t *testing.T) {
	vault := cliVaultWithPlugin(t, "demo", false)
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"uninstall", "--dry-run", "demo"}, vault, &stdout, &stderr)
	if exitCode != 0 || !strings.Contains(stdout.String(), "uninstall") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(vault, ".obsidian", "plugins", "demo")); err != nil {
		t.Fatalf("dry-run changed Vault: %v", err)
	}
}

func TestExportWritesPluginListPath(t *testing.T) {
	vault := emptyVault(t)
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"export", "saved.plugins"}, vault, &stdout, &stderr)
	if exitCode != 0 || !strings.Contains(stdout.String(), "Exported 0 plugins") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(vault, "saved.plugins")); err != nil {
		t.Fatalf("export missing: %v", err)
	}
}

func TestHelpListsUpdateCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exitCode := run(nil, t.TempDir(), &stdout, &stderr); exitCode != 0 {
		t.Fatalf("exit code = %d", exitCode)
	}
	if !strings.Contains(stdout.String(), "update") {
		t.Fatalf("help = %q", stdout.String())
	}
}

func TestHelpFlagPrintsHelpAndSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"--help"}, t.TempDir(), &stdout, &stderr)
	if exitCode != 0 || !strings.Contains(stdout.String(), "Usage: plugman") || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestShortHelpFlagPrintsHelpAndSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"-h"}, t.TempDir(), &stdout, &stderr)
	if exitCode != 0 || !strings.Contains(stdout.String(), "Usage: plugman") || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestHelpCommandPrintsHelpAndSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"help"}, t.TempDir(), &stdout, &stderr)
	if exitCode != 0 || !strings.Contains(stdout.String(), "Usage: plugman") || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestResolvedVersionSupportsReleaseBuildsAndGoInstall(t *testing.T) {
	tests := []struct {
		name, linked, module, want string
	}{
		{name: "release build", linked: "v1.2.3", module: "v9.9.9", want: "v1.2.3"},
		{name: "go install", linked: "dev", module: "v1.2.3", want: "v1.2.3"},
		{name: "local build", linked: "dev", module: "(devel)", want: "dev"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolvedVersion(test.linked, test.module); got != test.want {
				t.Fatalf("resolvedVersion(%q, %q) = %q, want %q", test.linked, test.module, got, test.want)
			}
		})
	}
}

func TestSubcommandHelpPrintsUsageAndSucceeds(t *testing.T) {
	commands := []string{"install", "update", "uninstall", "list", "outdated", "info", "export"}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run([]string{command, "--help"}, t.TempDir(), &stdout, &stderr)
			if exitCode != 0 || !strings.Contains(stderr.String(), "Usage of "+command) {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func emptyVault(t *testing.T) string {
	t.Helper()
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	return vault
}

func cliVaultWithPlugin(t *testing.T, id string, enabled bool) string {
	t.Helper()
	vault := emptyVault(t)
	plugin := filepath.Join(vault, ".obsidian", "plugins", id)
	if err := os.MkdirAll(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "manifest.json"), []byte(`{"id":"`+id+`","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	enabledJSON := "[]"
	if enabled {
		enabledJSON = `["` + id + `"]`
	}
	if err := os.WriteFile(filepath.Join(vault, ".obsidian", "community-plugins.json"), []byte(enabledJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return vault
}
