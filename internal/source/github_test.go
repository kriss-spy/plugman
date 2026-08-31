package source

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGitHubResolverResolvesExactPrerelease(t *testing.T) {
	t.Parallel()

	doer := &recordingDoer{responses: map[string]fakeResponse{
		"https://api.test/repos/old/plugin":                                          {body: `{"full_name":"new/plugin"}`},
		"https://api.test/repos/new/plugin/releases/tags/2.0.0-beta.1":               {body: `{"tag_name":"2.0.0-beta.1","html_url":"https://github.com/new/plugin/releases/tag/2.0.0-beta.1","prerelease":true,"assets":[{"name":"manifest.json","browser_download_url":"https://github.com/new/plugin/releases/download/2.0.0-beta.1/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/new/plugin/releases/download/2.0.0-beta.1/main.js"}]}`},
		"https://github.com/new/plugin/releases/download/2.0.0-beta.1/manifest.json": {body: `{"id":"plugin-id","name":"Plugin","version":"2.0.0-beta.1","minAppVersion":"1.5.0"}`},
	}}

	release, provenance, err := newGitHubTestResolver(doer).Resolve(context.Background(), "https://github.com/old/plugin/releases/tag/2.0.0-beta.1", Target{ObsidianVersion: "1.8.0"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if release.PluginID != "plugin-id" || release.Version != "2.0.0-beta.1" || release.Repository != "new/plugin" {
		t.Fatalf("release identity = %#v", release)
	}
	if provenance.Repository != "https://github.com/new/plugin" || provenance.Release != "2.0.0-beta.1" {
		t.Fatalf("provenance = %#v", provenance)
	}
	assertNeverDownloadedPluginCode(t, doer.requests)
}

func TestGitHubResolverSelectsNewestCompatibleStableRelease(t *testing.T) {
	t.Parallel()

	doer := &recordingDoer{responses: map[string]fakeResponse{
		"https://api.test/repos/acme/plugin": {body: `{"full_name":"acme-renamed/plugin"}`},
		"https://api.test/repos/acme-renamed/plugin/releases?per_page=100&page=1": {body: `[
			{"tag_name":"2.1.0-beta.1","html_url":"https://github.com/acme-renamed/plugin/releases/tag/2.1.0-beta.1","prerelease":true,"assets":[]},
			{"tag_name":"2.0.0","html_url":"https://github.com/acme-renamed/plugin/releases/tag/2.0.0","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/acme-renamed/plugin/releases/download/2.0.0/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/acme-renamed/plugin/releases/download/2.0.0/main.js"}]},
			{"tag_name":"1.10.0","html_url":"https://github.com/acme-renamed/plugin/releases/tag/1.10.0","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/acme-renamed/plugin/releases/download/1.10.0/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/acme-renamed/plugin/releases/download/1.10.0/main.js"},{"name":"styles.css","browser_download_url":"https://github.com/acme-renamed/plugin/releases/download/1.10.0/styles.css"}]},
			{"tag_name":"1.9.0","html_url":"https://github.com/acme-renamed/plugin/releases/tag/1.9.0","draft":true,"assets":[]}
		]`},
		"https://github.com/acme-renamed/plugin/releases/download/2.0.0/manifest.json":  {body: `{"id":"plugin-id","name":"Plugin","version":"2.0.0","minAppVersion":"2.0.0"}`},
		"https://github.com/acme-renamed/plugin/releases/download/1.10.0/manifest.json": {body: `{"id":"plugin-id","name":"Plugin","version":"1.10.0","minAppVersion":"1.7.0"}`},
	}}

	release, provenance, err := newGitHubTestResolver(doer).Resolve(context.Background(), "https://github.com/acme/plugin", Target{ObsidianVersion: "1.8.0"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if release.Version != "1.10.0" || release.MinimumObsidianVersion != "1.7.0" || release.Assets.Styles == nil {
		t.Fatalf("selected release = %#v", release)
	}
	if provenance.Repository != "https://github.com/acme-renamed/plugin" || provenance.Release != "1.10.0" {
		t.Fatalf("provenance = %#v", provenance)
	}
	assertNeverDownloadedPluginCode(t, doer.requests)
}

func TestGitHubResolverAcceptsManifestWithoutMinAppVersion(t *testing.T) {
	t.Parallel()

	doer := &recordingDoer{responses: map[string]fakeResponse{
		"https://api.test/repos/acme/plugin":                                   {body: `{"full_name":"acme/plugin"}`},
		"https://api.test/repos/acme/plugin/releases/tags/1.0.0":               {body: `{"tag_name":"1.0.0","html_url":"https://github.com/acme/plugin/releases/tag/1.0.0","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/acme/plugin/releases/download/1.0.0/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/acme/plugin/releases/download/1.0.0/main.js"}]}`},
		"https://github.com/acme/plugin/releases/download/1.0.0/manifest.json": {body: `{"id":"plugin","name":"Plugin","version":"1.0.0"}`},
	}}

	release, _, err := newGitHubTestResolver(doer).Resolve(context.Background(), "https://github.com/acme/plugin/releases/tag/1.0.0", Target{ObsidianVersion: "1.8.0"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if release.PluginID != "plugin" || release.Version != "1.0.0" || release.MinimumObsidianVersion != "" {
		t.Fatalf("release = %#v", release)
	}
}

func TestGitHubResolverRejectsMalformedInputsAndReleases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		response map[string]fakeResponse
		wantCode ErrorCode
	}{
		{name: "non HTTPS URL", input: "http://github.com/acme/plugin", wantCode: ErrorInvalidGitHubURL},
		{name: "unsupported tree URL", input: "https://github.com/acme/plugin/tree/main", wantCode: ErrorInvalidGitHubURL},
		{
			name:  "missing main asset",
			input: "https://github.com/acme/plugin/releases/tag/1.0.0",
			response: map[string]fakeResponse{
				"https://api.test/repos/acme/plugin":                     {body: `{"full_name":"acme/plugin"}`},
				"https://api.test/repos/acme/plugin/releases/tags/1.0.0": {body: `{"tag_name":"1.0.0","html_url":"https://github.com/acme/plugin/releases/tag/1.0.0","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/acme/plugin/releases/download/1.0.0/manifest.json"}]}`},
			},
			wantCode: ErrorMalformedMetadata,
		},
		{
			name:  "tag and manifest differ",
			input: "https://github.com/acme/plugin/releases/tag/1.0.0",
			response: map[string]fakeResponse{
				"https://api.test/repos/acme/plugin":                                   {body: `{"full_name":"acme/plugin"}`},
				"https://api.test/repos/acme/plugin/releases/tags/1.0.0":               {body: `{"tag_name":"1.0.0","html_url":"https://github.com/acme/plugin/releases/tag/1.0.0","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/acme/plugin/releases/download/1.0.0/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/acme/plugin/releases/download/1.0.0/main.js"}]}`},
				"https://github.com/acme/plugin/releases/download/1.0.0/manifest.json": {body: `{"id":"plugin-id","name":"Plugin","version":"1.0.1","minAppVersion":"1.0.0"}`},
			},
			wantCode: ErrorMalformedMetadata,
		},
		{
			name:  "incompatible exact release",
			input: "https://github.com/acme/plugin/releases/tag/1.0.0",
			response: map[string]fakeResponse{
				"https://api.test/repos/acme/plugin":                                   {body: `{"full_name":"acme/plugin"}`},
				"https://api.test/repos/acme/plugin/releases/tags/1.0.0":               {body: `{"tag_name":"1.0.0","html_url":"https://github.com/acme/plugin/releases/tag/1.0.0","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/acme/plugin/releases/download/1.0.0/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/acme/plugin/releases/download/1.0.0/main.js"}]}`},
				"https://github.com/acme/plugin/releases/download/1.0.0/manifest.json": {body: `{"id":"plugin-id","name":"Plugin","version":"1.0.0","minAppVersion":"2.0.0"}`},
			},
			wantCode: ErrorNoCompatibleRelease,
		},
		{
			name:  "arbitrary HTTPS asset host",
			input: "https://github.com/acme/plugin/releases/tag/1.0.0",
			response: map[string]fakeResponse{
				"https://api.test/repos/acme/plugin":                     {body: `{"full_name":"acme/plugin"}`},
				"https://api.test/repos/acme/plugin/releases/tags/1.0.0": {body: `{"tag_name":"1.0.0","html_url":"https://github.com/acme/plugin/releases/tag/1.0.0","assets":[{"name":"manifest.json","browser_download_url":"https://evil.example/manifest.json"},{"name":"main.js","browser_download_url":"https://evil.example/main.js"}]}`},
			},
			wantCode: ErrorMalformedMetadata,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doer := &recordingDoer{responses: tt.response}
			_, _, err := newGitHubTestResolver(doer).Resolve(context.Background(), tt.input, Target{ObsidianVersion: "1.8.0"})
			var sourceErr *Error
			if !errors.As(err, &sourceErr) || sourceErr.Code != tt.wantCode {
				t.Fatalf("Resolve() error = %T %v, want code %q", err, err, tt.wantCode)
			}
			assertNeverDownloadedPluginCode(t, doer.requests)
		})
	}
}

func TestGitHubReleaseAssetURLTrustBoundary(t *testing.T) {
	t.Parallel()

	if err := ValidateReleaseAssetURL("https://github.com/acme/plugin/releases/download/1.2.3/main.js", "acme/plugin", "1.2.3", "main.js"); err != nil {
		t.Fatalf("documented browser_download_url rejected: %v", err)
	}
	for _, value := range []string{
		"https://evil.example/acme/plugin/releases/download/1.2.3/main.js",
		"https://github.com/other/plugin/releases/download/1.2.3/main.js",
		"https://github.com/acme/plugin/releases/download/9.9.9/main.js",
		"https://github.com/acme/plugin/releases/download/1.2.3/manifest.json",
	} {
		if err := ValidateReleaseAssetURL(value, "acme/plugin", "1.2.3", "main.js"); err == nil {
			t.Fatalf("untrusted metadata URL accepted: %s", value)
		}
	}

	for _, value := range []string{
		"https://release-assets.githubusercontent.com/github-production-release-asset/file?sp=r",
		"https://objects.githubusercontent.com/github-production-release-asset/file?sig=x",
		"https://objects-origin.githubusercontent.com/github-production-release-asset/file?sig=x",
		"https://github-releases.githubusercontent.com/file?sig=x",
	} {
		if err := ValidateReleaseAssetRedirectURL(value); err != nil {
			t.Fatalf("documented GitHub release redirect rejected for %s: %v", value, err)
		}
	}
	if err := ValidateReleaseAssetRedirectURL("https://evil.example/github-production-release-asset/file"); err == nil {
		t.Fatal("arbitrary HTTPS redirect host accepted")
	}
}

func TestGitHubResolverRestrictsReleaseManifestRedirect(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		finalURL  string
		wantError bool
	}{
		{name: "GitHub release CDN", finalURL: "https://release-assets.githubusercontent.com/github-production-release-asset/file?sig=x"},
		{name: "arbitrary HTTPS host", finalURL: "https://evil.example/manifest.json", wantError: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifestURL := "https://github.com/acme/plugin/releases/download/1.0.0/manifest.json"
			doer := &recordingDoer{responses: map[string]fakeResponse{
				"https://api.test/repos/acme/plugin":                     {body: `{"full_name":"acme/plugin"}`},
				"https://api.test/repos/acme/plugin/releases/tags/1.0.0": {body: `{"tag_name":"1.0.0","html_url":"https://github.com/acme/plugin/releases/tag/1.0.0","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/acme/plugin/releases/download/1.0.0/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/acme/plugin/releases/download/1.0.0/main.js"}]}`},
				manifestURL: {body: `{"id":"plugin","name":"Plugin","version":"1.0.0","minAppVersion":"1.0.0"}`, finalURL: test.finalURL},
			}}
			_, _, err := newGitHubTestResolver(doer).Resolve(context.Background(), "https://github.com/acme/plugin/releases/tag/1.0.0", Target{ObsidianVersion: "1.8.0"})
			if test.wantError {
				assertSourceError(t, err, ErrorMalformedMetadata, 0)
			} else if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
		})
	}
}

func newGitHubTestResolver(client HTTPDoer) *GitHubResolver {
	return NewGitHubResolver(GitHubConfig{Client: client, APIBaseURL: "https://api.test"})
}

func assertNeverDownloadedPluginCode(t *testing.T, requests []string) {
	t.Helper()
	for _, endpoint := range requests {
		if strings.HasSuffix(endpoint, "/main.js") || strings.HasSuffix(endpoint, "/styles.css") {
			t.Fatalf("resolver downloaded plugin code from %q", endpoint)
		}
	}
}

type fakeResponse struct {
	status   int
	body     string
	header   http.Header
	finalURL string
}

type recordingDoer struct {
	responses map[string]fakeResponse
	requests  []string
}

func (d *recordingDoer) Do(request *http.Request) (*http.Response, error) {
	d.requests = append(d.requests, request.URL.String())
	response, ok := d.responses[request.URL.String()]
	if !ok {
		return nil, errors.New("unexpected request: " + request.URL.String())
	}
	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	finalRequest := request
	if response.finalURL != "" {
		var err error
		finalRequest, err = http.NewRequestWithContext(request.Context(), request.Method, response.finalURL, nil)
		if err != nil {
			return nil, err
		}
	}
	return &http.Response{
		StatusCode: status,
		Header:     response.header,
		Body:       io.NopCloser(strings.NewReader(response.body)),
		Request:    finalRequest,
	}, nil
}
