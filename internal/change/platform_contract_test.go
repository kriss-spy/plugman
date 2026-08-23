package change

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestPlatformClosedVaultContract runs natively on every supported desktop OS.
// It guards the filesystem assumptions used by the closed-Vault adapter without
// weakening the release gate for any one platform.
func TestPlatformClosedVaultContract(t *testing.T) {
	t.Run("rejects platform separators before creating recovery state", func(t *testing.T) {
		for _, pluginID := range []string{"nested/plugin", `nested\plugin`} {
			vault := newVault(t, nil)
			stage := filepath.Join(vault, "download")
			writePlugin(t, stage, "nested", "1.0.0", false)

			if _, err := New().Apply(context.Background(), PreparedChange{
				VaultRoot: vault,
				PluginID:  pluginID,
				Kind:      Install,
				StagedDir: stage,
			}); err == nil {
				t.Fatalf("plugin ID %q accepted a path separator", pluginID)
			}
			if _, err := os.Lstat(filepath.Join(vault, ".obsidian", ".plugman")); !os.IsNotExist(err) {
				t.Fatalf("unsafe plugin ID %q created mutation state: %v", pluginID, err)
			}
		}
	})

	t.Run("installs through Vault-local replacement and retains asset permissions", func(t *testing.T) {
		vault := newVault(t, nil)
		stage := filepath.Join(t.TempDir(), "download")
		writePlugin(t, stage, "demo", "1.0.0", true)
		stagedMain := filepath.Join(stage, "main.js")
		if err := os.Chmod(stagedMain, 0o400); err != nil {
			t.Fatal(err)
		}
		stagedInfo, err := os.Stat(stagedMain)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := New().Apply(context.Background(), PreparedChange{
			VaultRoot: vault,
			PluginID:  "demo",
			Kind:      Install,
			StagedDir: stage,
		}); err != nil {
			t.Fatal(err)
		}

		installedMain := filepath.Join(vault, ".obsidian", "plugins", "demo", "main.js")
		installedInfo, err := os.Stat(installedMain)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := installedInfo.Mode().Perm(), stagedInfo.Mode().Perm(); got != want {
			t.Fatalf("installed main.js permissions = %v, want %v", got, want)
		}
		if _, err := os.Stat(stagedMain); err != nil {
			t.Fatalf("staged input was mutated: %v", err)
		}
		assertNoRecovery(t, vault)
	})
}
