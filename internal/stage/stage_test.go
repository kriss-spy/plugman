package stage

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kriss-spy/plugman/internal/source"
)

func TestStageDownloadsAndValidatesReleaseAssets(t *testing.T) {
	t.Parallel()
	server := assetServer(t, map[string]string{
		"/manifest.json": `{"id":"demo","name":"Demo","version":"2.0.0","minAppVersion":"1.5.0"}`,
		"/main.js":       "plugin code",
		"/styles.css":    "body {}",
	})
	vault := testVault(t)
	destination := filepath.Join(vault, ".obsidian", ".plugman", "staging", "operation", "demo")

	prepared, err := New(Config{Client: server.Client(), AllowedTestOrigins: []string{server.URL}}).Stage(
		context.Background(), Request{VaultRoot: vault, Destination: destination, Release: testRelease(server.URL, "demo", "2.0.0")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Dir != destination || prepared.Release.PluginID != "demo" {
		t.Fatalf("unexpected prepared release: %+v", prepared)
	}
	assertContents(t, filepath.Join(destination, "manifest.json"), `{"id":"demo","name":"Demo","version":"2.0.0","minAppVersion":"1.5.0"}`)
	assertContents(t, filepath.Join(destination, "main.js"), "plugin code")
	assertContents(t, filepath.Join(destination, "styles.css"), "body {}")
}

func TestStageRejectsManifestMismatchAndCleansPartialDestination(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		problem  string
	}{
		{"id", `{"id":"other","name":"Other","version":"2.0.0","minAppVersion":"1.5.0"}`, "manifest id"},
		{"version", `{"id":"demo","name":"Demo","version":"3.0.0","minAppVersion":"1.5.0"}`, "manifest version"},
		{"minimum Obsidian version", `{"id":"demo","name":"Demo","version":"2.0.0","minAppVersion":"1.6.0"}`, "manifest minAppVersion"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := assetServer(t, map[string]string{"/manifest.json": test.manifest, "/main.js": "plugin code"})
			vault := testVault(t)
			destination := filepath.Join(vault, ".obsidian", ".plugman", "staging", "demo")
			release := testRelease(server.URL, "demo", "2.0.0")
			release.Assets.Styles = nil

			_, err := New(Config{Client: server.Client(), AllowedTestOrigins: []string{server.URL}}).Stage(
				context.Background(), Request{VaultRoot: vault, Destination: destination, Release: release},
			)
			if err == nil || !strings.Contains(err.Error(), test.problem) {
				t.Fatalf("expected %s mismatch, got %v", test.problem, err)
			}
			if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
				t.Fatalf("partial destination remains: %v", statErr)
			}
		})
	}
}

func TestStageRejectsOversizedAssetsAndNonHTTPSURLs(t *testing.T) {
	t.Parallel()
	server := assetServer(t, map[string]string{
		"/manifest.json": `{"id":"demo","name":"Demo","version":"2.0.0","minAppVersion":"1.5.0"}`,
		"/main.js":       "too large",
	})
	vault := testVault(t)
	release := testRelease(server.URL, "demo", "2.0.0")
	release.Assets.Styles = nil

	_, err := New(Config{Client: server.Client(), AllowedTestOrigins: []string{server.URL}, MaxMainJSBytes: 3}).Stage(
		context.Background(), Request{VaultRoot: vault, Destination: filepath.Join(vault, ".obsidian", ".plugman", "staging", "large"), Release: release},
	)
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("expected size rejection, got %v", err)
	}

	_, err = New(Config{Client: server.Client()}).Stage(
		context.Background(), Request{VaultRoot: vault, Destination: filepath.Join(vault, ".obsidian", ".plugman", "staging", "http"), Release: release},
	)
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected HTTPS rejection, got %v", err)
	}
}

func TestStageRejectsUnsafeDestinationWithoutTouchingOutsideFile(t *testing.T) {
	t.Parallel()
	vault := testVault(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(Config{}).Stage(context.Background(), Request{
		VaultRoot: vault, Destination: outside, Release: testRelease("https://assets.test", "demo", "1.0.0"),
	})
	if err == nil || !strings.Contains(err.Error(), "staging area") {
		t.Fatalf("expected unsafe destination rejection, got %v", err)
	}
	assertContents(t, outside, "keep")

	staging := filepath.Join(vault, ".obsidian", ".plugman", "staging")
	if err := os.MkdirAll(filepath.Dir(staging), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), staging); err != nil {
		t.Fatal(err)
	}
	_, err = New(Config{}).Stage(context.Background(), Request{
		VaultRoot: vault, Destination: filepath.Join(staging, "demo"), Release: testRelease("https://assets.test", "demo", "1.0.0"),
	})
	if err == nil || !strings.Contains(err.Error(), "regular directory") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestPreflightStagesWholeBatchInDeclarationOrderAndCleansOnFailure(t *testing.T) {
	t.Parallel()
	server := assetServer(t, map[string]string{
		"/first/manifest.json":  `{"id":"first","name":"First","version":"1.0.0","minAppVersion":"1.5.0"}`,
		"/first/main.js":        "first",
		"/second/manifest.json": `{"id":"second","name":"Second","version":"1.0.0","minAppVersion":"1.5.0"}`,
		"/second/main.js":       "second",
	})
	vault := testVault(t)
	first := testRelease(server.URL+"/first", "first", "1.0.0")
	first.Assets.Styles = nil
	second := testRelease(server.URL+"/second", "second", "1.0.0")
	second.Assets.Styles = nil
	stager := New(Config{Client: server.Client(), AllowedTestOrigins: []string{server.URL}})

	prepared, err := stager.Preflight(context.Background(), vault, []source.Release{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 2 || prepared[0].Release.PluginID != "second" || prepared[1].Release.PluginID != "first" {
		t.Fatalf("declaration order lost: %+v", prepared)
	}
	batchRoot := prepared[0].BatchRoot
	if err := prepared[0].Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(batchRoot); !os.IsNotExist(err) {
		t.Fatalf("batch cleanup failed: %v", err)
	}

	bad := second
	bad.Assets.MainJS.URL = server.URL + "/missing"
	_, err = stager.Preflight(context.Background(), vault, []source.Release{first, bad})
	if err == nil {
		t.Fatal("expected batch failure")
	}
	entries, readErr := os.ReadDir(filepath.Join(vault, ".obsidian", ".plugman", "staging"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed preflight retained staging: %v", entries)
	}
}

func TestPreflightDryRunUsesDisposableExternalStagingAndCleansIt(t *testing.T) {
	server := assetServer(t, map[string]string{
		"/manifest.json": `{"id":"demo","name":"Demo","version":"1.0.0","minAppVersion":"1.5.0"}`,
		"/main.js":       "plugin",
	})
	vault := testVault(t)
	release := testRelease(server.URL, "demo", "1.0.0")
	release.Assets.Styles = nil
	stager := New(Config{Client: server.Client(), AllowedTestOrigins: []string{server.URL}})

	prepared, err := stager.PreflightDryRun(context.Background(), vault, []source.Release{release})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 1 {
		t.Fatalf("prepared = %#v", prepared)
	}
	if relative, err := filepath.Rel(vault, prepared[0].Dir); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("dry-run staging is inside Vault: %s", prepared[0].Dir)
	}
	if _, err := os.Lstat(filepath.Join(vault, ".obsidian", ".plugman")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created Vault-local Plugman state: %v", err)
	}
	temporaryRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(prepared[0].Dir)))))
	if err := prepared[0].Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(temporaryRoot); !os.IsNotExist(err) {
		t.Fatalf("disposable dry-run root remains: %v", err)
	}
}

func TestStageRejectsArbitraryHTTPSAssetMetadataBeforeDownload(t *testing.T) {
	t.Parallel()

	vault := testVault(t)
	release := testRelease("https://evil.example", "demo", "1.0.0")
	release.Repository = "acme/demo"
	release.Assets.Styles = nil
	client := doerFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("untrusted asset URL reached HTTP client")
		return nil, nil
	})
	_, err := New(Config{Client: client}).Stage(context.Background(), Request{
		VaultRoot: vault, Destination: filepath.Join(vault, ".obsidian", ".plugman", "staging", "malicious"), Release: release,
	})
	if err == nil || !strings.Contains(err.Error(), "GitHub release") {
		t.Fatalf("expected arbitrary host rejection, got %v", err)
	}
}

func TestStageAllowsGitHubReleaseCDNRedirectAndRejectsArbitraryRedirect(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		finalHost string
		wantError bool
	}{
		{name: "documented GitHub CDN", finalHost: "release-assets.githubusercontent.com"},
		{name: "arbitrary host", finalHost: "evil.example", wantError: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			vault := testVault(t)
			release := testRelease("https://github.com/acme/demo/releases/download/1.0.0", "demo", "1.0.0")
			release.Repository = "acme/demo"
			release.Assets.Styles = nil
			client := doerFunc(func(request *http.Request) (*http.Response, error) {
				body := "plugin code"
				if strings.HasSuffix(request.URL.Path, "/manifest.json") {
					body = `{"id":"demo","name":"Demo","version":"1.0.0","minAppVersion":"1.5.0"}`
				}
				finalRequest, err := http.NewRequestWithContext(request.Context(), request.Method, "https://"+test.finalHost+"/github-production-release-asset/file?sig=x", nil)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: finalRequest}, nil
			})
			_, err := New(Config{Client: client}).Stage(context.Background(), Request{
				VaultRoot: vault, Destination: filepath.Join(vault, ".obsidian", ".plugman", "staging", "redirect"), Release: release,
			})
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "redirect host") {
					t.Fatalf("expected arbitrary redirect rejection, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("documented GitHub redirect rejected: %v", err)
			}
		})
	}
}

func testVault(t *testing.T) string {
	t.Helper()
	vault := t.TempDir()
	if err := os.Mkdir(filepath.Join(vault, ".obsidian"), 0o700); err != nil {
		t.Fatal(err)
	}
	return vault
}

func testRelease(base, id, version string) source.Release {
	return source.Release{
		PluginID: id, Repository: "owner/" + id, Version: version, MinimumObsidianVersion: "1.5.0",
		Assets: source.ReleaseAssets{
			Manifest: source.AssetRef{Name: "manifest.json", URL: base + "/manifest.json"},
			MainJS:   source.AssetRef{Name: "main.js", URL: base + "/main.js"},
			Styles:   &source.AssetRef{Name: "styles.css", URL: base + "/styles.css"},
		},
	}
}

type assetService struct {
	URL    string
	assets map[string]string
}

type doerFunc func(*http.Request) (*http.Response, error)

func (function doerFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func (service *assetService) Client() source.HTTPDoer {
	return doerFunc(func(request *http.Request) (*http.Response, error) {
		value, ok := service.assets[request.URL.Path]
		status := http.StatusOK
		if !ok {
			value = "not found"
			status = http.StatusNotFound
		}
		return &http.Response{
			StatusCode:    status,
			Body:          io.NopCloser(strings.NewReader(value)),
			ContentLength: int64(len(value)),
			Request:       request,
		}, nil
	})
}

func assetServer(t *testing.T, assets map[string]string) *assetService {
	t.Helper()
	return &assetService{URL: "http://assets.test", assets: assets}
}

func assertContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s = %q, err=%v, want %q", path, got, err, want)
	}
}
