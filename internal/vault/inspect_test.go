package vault_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kriss-spy/plugman/internal/model"
	"github.com/kriss-spy/plugman/internal/vault"
)

func TestInspectRefusesPendingRecoveryState(t *testing.T) {
	vaultRoot := t.TempDir()
	recoveryPath := filepath.Join(vaultRoot, ".obsidian", ".plugman", "recovery", "current")
	if err := os.MkdirAll(recoveryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	plugins, err := vault.Inspect(vaultRoot)
	var pending *model.PendingRecoveryError
	if !errors.As(err, &pending) || pending.Path != recoveryPath || plugins != nil {
		t.Fatalf("plugins = %#v, error = %v", plugins, err)
	}
}

func TestValidateRootOwnsObsidianDirectoryValidation(t *testing.T) {
	root := t.TempDir()
	configurationPath := filepath.Join(root, ".obsidian")

	err := vault.ValidateRoot(root)
	var validation *model.VaultValidationError
	if !errors.As(err, &validation) || validation.Path != configurationPath {
		t.Fatalf("ValidateRoot() error = %v", err)
	}

	if err := os.WriteFile(configurationPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = vault.ValidateRoot(root)
	if !errors.As(err, &validation) || validation.Path != configurationPath || validation.Reason != "not a directory" {
		t.Fatalf("ValidateRoot() file error = %v", err)
	}

	if err := os.Remove(configurationPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(configurationPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := vault.ValidateRoot(root); err != nil {
		t.Fatalf("ValidateRoot() valid Vault error = %v", err)
	}
}

func TestInspectSnapshotsPluginDataPresence(t *testing.T) {
	vaultRoot := sourceRecordVault(t, "https://github.com/owner/demo", "1.0.0")
	dataPath := filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "data.json")
	if err := os.WriteFile(dataPath, []byte(`{"setting":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plugins, err := vault.Inspect(vaultRoot)
	if err != nil || len(plugins) != 1 || plugins[0].HasData == nil || !*plugins[0].HasData {
		t.Fatalf("plugins = %#v, error = %v", plugins, err)
	}
	if err := os.Remove(dataPath); err != nil {
		t.Fatal(err)
	}
	plugins, err = vault.Inspect(vaultRoot)
	if err != nil || plugins[0].HasData == nil || *plugins[0].HasData {
		t.Fatalf("plugins without data = %#v, error = %v", plugins, err)
	}
}

func TestInspectRejectsInvalidGitHubSourceRecords(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		release    string
	}{
		{name: "http", repository: "http://github.com/owner/repo", release: "1.0.0"},
		{name: "credentials", repository: "https://user@github.com/owner/repo", release: "1.0.0"},
		{name: "port", repository: "https://github.com:443/owner/repo", release: "1.0.0"},
		{name: "uppercase host", repository: "https://GitHub.com/owner/repo", release: "1.0.0"},
		{name: "query", repository: "https://github.com/owner/repo?ref=main", release: "1.0.0"},
		{name: "empty query", repository: "https://github.com/owner/repo?", release: "1.0.0"},
		{name: "fragment", repository: "https://github.com/owner/repo#readme", release: "1.0.0"},
		{name: "extra path", repository: "https://github.com/owner/repo/releases", release: "1.0.0"},
		{name: "trailing slash", repository: "https://github.com/owner/repo/", release: "1.0.0"},
		{name: "owner traversal", repository: "https://github.com/../repo", release: "1.0.0"},
		{name: "encoded traversal", repository: "https://github.com/%2e%2e/repo", release: "1.0.0"},
		{name: "encoded separator", repository: "https://github.com/owner%2Frepo/name", release: "1.0.0"},
		{name: "empty release", repository: "https://github.com/owner/repo", release: ""},
		{name: "release slash", repository: "https://github.com/owner/repo", release: "feature/release"},
		{name: "release backslash", repository: "https://github.com/owner/repo", release: `feature\release`},
		{name: "release traversal", repository: "https://github.com/owner/repo", release: ".."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vaultRoot := sourceRecordVault(t, test.repository, test.release)
			plugins, err := vault.Inspect(vaultRoot)
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}
			if len(plugins) != 1 || plugins[0].Status != model.PluginInvalid || plugins[0].Source.Kind != model.SourceUnknown || len(plugins[0].Problems) != 1 || plugins[0].Problems[0].Code != "source_record_invalid" {
				t.Fatalf("plugins = %#v", plugins)
			}
		})
	}
}

func TestInspectAcceptsExactPublicGitHubSourceRecord(t *testing.T) {
	vaultRoot := sourceRecordVault(t, "https://github.com/owner-name/repo_name", "v1.2.3-beta.1")
	plugins, err := vault.Inspect(vaultRoot)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if len(plugins) != 1 || plugins[0].Status != model.PluginValid || plugins[0].Source.Kind != model.SourceGitHub || plugins[0].Source.Repository == nil || *plugins[0].Source.Repository != "https://github.com/owner-name/repo_name" || plugins[0].Source.Release == nil || *plugins[0].Source.Release != "v1.2.3-beta.1" {
		t.Fatalf("plugins = %#v", plugins)
	}
}

func sourceRecordVault(t *testing.T, repository, release string) string {
	t.Helper()
	vaultRoot := t.TempDir()
	pluginRoot := filepath.Join(vaultRoot, ".obsidian", "plugins", "demo")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "manifest.json"), []byte(`{"id":"demo","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(map[string]string{"repository": repository, "release": release})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".plugman.json"), record, 0o644); err != nil {
		t.Fatal(err)
	}
	return vaultRoot
}
