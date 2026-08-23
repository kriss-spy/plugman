package plugman_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUnixInstallerInstallsLatestVerifiedBinaryAndUpdatesPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	home, fixtures, fakeBin := unixInstallerFixture(t, false)
	command := exec.Command("sh", "install.sh")
	command.Dir = projectRoot(t)
	command.Env = installerEnv(home, fixtures, fakeBin)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, output)
	}
	installed, err := os.ReadFile(filepath.Join(home, ".local", "bin", "plugman"))
	if err != nil || string(installed) != "verified plugman binary\n" {
		t.Fatalf("installed binary = %q, %v", installed, err)
	}
	profile, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil || strings.Count(string(profile), "Added by Plugman installer") != 1 {
		t.Fatalf("profile = %q, %v", profile, err)
	}
	if !strings.Contains(string(output), "Installed Plugman v9.8.7") {
		t.Fatalf("output = %q", output)
	}
	command = exec.Command("sh", "install.sh")
	command.Dir = projectRoot(t)
	command.Env = installerEnv(home, fixtures, fakeBin)
	if secondOutput, secondErr := command.CombinedOutput(); secondErr != nil {
		t.Fatalf("second install.sh: %v\n%s", secondErr, secondOutput)
	}
	profile, err = os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil || strings.Count(string(profile), "Added by Plugman installer") != 1 {
		t.Fatalf("profile after second install = %q, %v", profile, err)
	}
}

func TestUnixInstallerRejectsChecksumMismatchWithoutReplacingBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	home, fixtures, fakeBin := unixInstallerFixture(t, true)
	installDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	installedPath := filepath.Join(installDir, "plugman")
	if err := os.WriteFile(installedPath, []byte("existing binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "install.sh", "--version", "v9.8.7")
	command.Dir = projectRoot(t)
	command.Env = installerEnv(home, fixtures, fakeBin)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "checksum") {
		t.Fatalf("install.sh error = %v, output = %q", err, output)
	}
	installed, readErr := os.ReadFile(installedPath)
	if readErr != nil || string(installed) != "existing binary\n" {
		t.Fatalf("existing binary changed: %q, %v", installed, readErr)
	}
}

func TestReleaseBoundUnixInstallerDoesNotResolveLatest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	home, fixtures, fakeBin := unixInstallerFixture(t, false)
	source, err := os.ReadFile(filepath.Join(projectRoot(t), "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	packagedPath := filepath.Join(t.TempDir(), "install.sh")
	packaged := strings.ReplaceAll(string(source), "@PLUGMAN_VERSION@", "v9.8.7")
	if err := os.WriteFile(packagedPath, []byte(packaged), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", packagedPath)
	command.Env = append(installerEnv(home, fixtures, fakeBin), "PLUGMAN_FAIL_LATEST=1")
	if output, runErr := command.CombinedOutput(); runErr != nil {
		t.Fatalf("release-bound install.sh resolved latest: %v\n%s", runErr, output)
	}
}

func TestUnixInstallerRefusesDestinationInsideVault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	home, fixtures, fakeBin := unixInstallerFixture(t, false)
	vaultRoot := filepath.Join(home, "vault")
	if err := os.MkdirAll(filepath.Join(vaultRoot, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	installDir := filepath.Join(vaultRoot, "bin")
	command := exec.Command("sh", "install.sh", "--version", "v9.8.7", "--bin-dir", installDir)
	command.Dir = projectRoot(t)
	command.Env = installerEnv(home, fixtures, fakeBin)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Obsidian Vault") {
		t.Fatalf("install.sh error = %v, output = %q", err, output)
	}
	if _, statErr := os.Stat(filepath.Join(installDir, "plugman")); !os.IsNotExist(statErr) {
		t.Fatalf("Vault executable exists after refusal: %v", statErr)
	}
}

func TestUnixInstallerAddsCustomDirectoryWithSpacesToFishPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	home, fixtures, fakeBin := unixInstallerFixture(t, false)
	installDir := filepath.Join(home, "custom tools", "bin")
	command := exec.Command("sh", "install.sh", "--version", "v9.8.7", "--bin-dir", installDir)
	command.Dir = projectRoot(t)
	command.Env = append(installerEnv(home, fixtures, fakeBin), "SHELL=/usr/bin/fish")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("install.sh: %v\n%s", err, output)
	}
	profile, err := os.ReadFile(filepath.Join(home, ".config", "fish", "config.fish"))
	if err != nil || !strings.Contains(string(profile), "fish_add_path '"+installDir+"'") {
		t.Fatalf("fish profile = %q, %v", profile, err)
	}
}

func unixInstallerFixture(t *testing.T, badChecksum bool) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	fixtures := filepath.Join(root, "fixtures")
	fakeBin := filepath.Join(root, "fake-bin")
	for _, directory := range []string{home, fixtures, fakeBin} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	architecture := "amd64"
	machine := "x86_64"
	if runtime.GOARCH == "arm64" {
		architecture = "arm64"
		machine = "arm64"
	}
	asset := "plugman_v9.8.7_" + runtime.GOOS + "_" + architecture
	contents := []byte("verified plugman binary\n")
	if err := os.WriteFile(filepath.Join(fixtures, asset), contents, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(contents))
	if badChecksum {
		digest = strings.Repeat("0", 64)
	}
	if err := os.WriteFile(filepath.Join(fixtures, "checksums.txt"), []byte(digest+"  "+asset+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	curl := `#!/bin/sh
output=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) output=$2; shift 2 ;;
    --write-out) shift 2 ;;
    --*) shift ;;
    *) url=$1; shift ;;
  esac
done
case "$url" in
  */releases/latest)
    [ -z "${PLUGMAN_FAIL_LATEST:-}" ] || exit 88
    printf '%s' 'https://github.com/kriss-spy/plugman/releases/tag/v9.8.7'
    ;;
  */checksums.txt) cp "$PLUGMAN_TEST_FIXTURES/checksums.txt" "$output" ;;
  *) cp "$PLUGMAN_TEST_FIXTURES/${url##*/}" "$output" ;;
esac
`
	if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte(curl), 0o755); err != nil {
		t.Fatal(err)
	}
	uname := "#!/bin/sh\nif [ \"$1\" = \"-s\" ]; then printf '%s\\n' " + shellQuote(runtime.GOOS) + "; else printf '%s\\n' " + shellQuote(machine) + "; fi\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "uname"), []byte(uname), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, fixtures, fakeBin
}

func installerEnv(home, fixtures, fakeBin string) []string {
	path := fakeBin + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
	return []string{
		"HOME=" + home,
		"PATH=" + path,
		"SHELL=/bin/zsh",
		"PLUGMAN_TEST_FIXTURES=" + fixtures,
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
