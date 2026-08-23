package manager_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/kriss-spy/plugman/internal/manager"
	"github.com/kriss-spy/plugman/internal/model"
)

func TestListRejectsDirectoryWithoutObsidianConfiguration(t *testing.T) {
	vaultRoot := t.TempDir()

	_, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})

	var validationError *model.VaultValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("Run() error = %v, want VaultValidationError", err)
	}
	wantPath := filepath.Join(vaultRoot, ".obsidian")
	if validationError.Path != wantPath {
		t.Errorf("validation path = %q, want %q", validationError.Path, wantPath)
	}
}

func TestListReportsInstalledPluginState(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "dataview", "manifest.json"), `{"id":"dataview","version":"0.5.67"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "community-plugins.json"), `["dataview"]`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []model.PluginObservation{{
		Folder:   "dataview",
		ID:       pointer("dataview"),
		Version:  pointer("0.5.67"),
		Enabled:  pointer(true),
		Source:   model.PluginSource{Kind: model.SourceUnknown},
		Status:   model.PluginValid,
		Problems: []model.Problem{},
	}}
	if !reflect.DeepEqual(report.Plugins, want) {
		t.Errorf("plugins = %#v, want %#v", report.Plugins, want)
	}
}

func TestListReportsFolderWithMissingManifestAsInvalidPluginState(t *testing.T) {
	vaultRoot := newVault(t)
	if err := os.MkdirAll(filepath.Join(vaultRoot, ".obsidian", "plugins", "broken"), 0o755); err != nil {
		t.Fatalf("create plugin folder: %v", err)
	}

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []model.PluginObservation{{
		Folder:   "broken",
		Source:   model.PluginSource{Kind: model.SourceUnknown},
		Status:   model.PluginInvalid,
		Problems: []model.Problem{{Code: "manifest_missing", Message: "manifest.json is missing"}},
	}}
	if !reflect.DeepEqual(report.Plugins, want) {
		t.Errorf("plugins = %#v, want %#v", report.Plugins, want)
	}
}

func TestListReportsInvalidManifestJSON(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "broken", "manifest.json"), `{`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(report.Plugins) != 1 {
		t.Fatalf("len(plugins) = %d, want 1", len(report.Plugins))
	}
	plugin := report.Plugins[0]
	if plugin.Status != model.PluginInvalid {
		t.Errorf("status = %q, want %q", plugin.Status, model.PluginInvalid)
	}
	if len(plugin.Problems) != 1 || plugin.Problems[0].Code != "manifest_invalid_json" {
		t.Errorf("problems = %#v, want manifest_invalid_json", plugin.Problems)
	}
}

func TestListReportsMissingManifestIdentity(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "broken", "manifest.json"), `{"version":"1.0.0"}`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	plugin := report.Plugins[0]
	if plugin.Status != model.PluginInvalid || plugin.ID != nil || plugin.Version == nil {
		t.Errorf("plugin = %#v, want invalid state with unknown ID and known version", plugin)
	}
	if len(plugin.Problems) != 1 || plugin.Problems[0].Code != "manifest_id_invalid" {
		t.Errorf("problems = %#v, want manifest_id_invalid", plugin.Problems)
	}
}

func TestListReportsMissingManifestVersion(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "broken", "manifest.json"), `{"id":"broken"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "community-plugins.json"), `["broken"]`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	plugin := report.Plugins[0]
	if plugin.Status != model.PluginInvalid || plugin.ID == nil || plugin.Version != nil || plugin.Enabled == nil || !*plugin.Enabled {
		t.Errorf("plugin = %#v, want invalid enabled state with known ID", plugin)
	}
	if len(plugin.Problems) != 1 || plugin.Problems[0].Code != "manifest_version_invalid" {
		t.Errorf("problems = %#v, want manifest_version_invalid", plugin.Problems)
	}
}

func TestListReportsWrongManifestFieldType(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "broken", "manifest.json"), `{"id":42,"version":"1.0.0"}`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	plugin := report.Plugins[0]
	if len(plugin.Problems) != 1 || plugin.Problems[0].Code != "manifest_id_invalid" {
		t.Errorf("problems = %#v, want manifest_id_invalid", plugin.Problems)
	}
}

func TestListReportsManifestIdentityFolderMismatch(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "wrong-folder", "manifest.json"), `{"id":"actual-id","version":"1.0.0"}`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	plugin := report.Plugins[0]
	if plugin.Status != model.PluginInvalid || plugin.ID == nil || *plugin.ID != "actual-id" {
		t.Errorf("plugin = %#v, want invalid state retaining actual ID", plugin)
	}
	if len(plugin.Problems) != 1 || plugin.Problems[0].Code != "manifest_id_mismatch" {
		t.Errorf("problems = %#v, want manifest_id_mismatch", plugin.Problems)
	}
}

func TestListReportsGitHubSourceRecord(t *testing.T) {
	vaultRoot := newVault(t)
	pluginRoot := filepath.Join(vaultRoot, ".obsidian", "plugins", "opencode")
	writeFile(t, filepath.Join(pluginRoot, "manifest.json"), `{"id":"opencode","version":"1.3.13"}`)
	writeFile(t, filepath.Join(pluginRoot, ".plugman.json"), `{"repository":"https://github.com/kriss-spy/obsidian-opencode","release":"1.3.13"}`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	wantRepository := "https://github.com/kriss-spy/obsidian-opencode"
	wantRelease := "1.3.13"
	want := model.PluginSource{Kind: model.SourceGitHub, Repository: &wantRepository, Release: &wantRelease}
	if !reflect.DeepEqual(report.Plugins[0].Source, want) {
		t.Errorf("source = %#v, want %#v", report.Plugins[0].Source, want)
	}
}

func TestListRejectsUntrustedSourceRecordRepository(t *testing.T) {
	vaultRoot := newVault(t)
	pluginRoot := filepath.Join(vaultRoot, ".obsidian", "plugins", "opencode")
	writeFile(t, filepath.Join(pluginRoot, "manifest.json"), `{"id":"opencode","version":"1.3.13"}`)
	writeFile(t, filepath.Join(pluginRoot, ".plugman.json"), `{"repository":"https://example.com/owner/repo","release":"1.3.13"}`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	plugin := report.Plugins[0]
	if plugin.Status != model.PluginInvalid || len(plugin.Problems) != 1 || plugin.Problems[0].Code != "source_record_invalid" {
		t.Errorf("plugin = %#v, want invalid source record", plugin)
	}
}

func TestListEnabledOnlyFiltersOnKnownEnabledState(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "enabled", "manifest.json"), `{"id":"enabled","version":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "disabled", "manifest.json"), `{"id":"disabled","version":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "unknown", "manifest.json"), `{`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "community-plugins.json"), `["enabled"]`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{
		Kind: model.OperationList,
		List: model.ListOptions{EnabledOnly: true},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Plugins) != 1 || report.Plugins[0].ID == nil || *report.Plugins[0].ID != "enabled" {
		t.Errorf("plugins = %#v, want only enabled", report.Plugins)
	}
}

func TestListReportsSymlinkedPluginDirectoryAsInvalid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires additional privileges on Windows")
	}
	vaultRoot := newVault(t)
	target := t.TempDir()
	pluginPath := filepath.Join(vaultRoot, ".obsidian", "plugins", "linked")
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, pluginPath); err != nil {
		t.Fatal(err)
	}

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Plugins) != 1 || report.Plugins[0].Problems[0].Code != "plugin_directory_symlink" {
		t.Errorf("plugins = %#v, want explicit symlink problem", report.Plugins)
	}
}

func newVault(t *testing.T) string {
	t.Helper()
	vaultRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vaultRoot, ".obsidian"), 0o755); err != nil {
		t.Fatalf("create Vault: %v", err)
	}
	return vaultRoot
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func pointer[T any](value T) *T {
	return &value
}
