package source

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
)

const officialRegistryURL = "https://registry.test/community-plugins.json"

func TestOfficialResolverUsesCanonicalTransferAndCompatibleRootWithoutVersions(t *testing.T) {
	t.Parallel()

	manifestAsset := "https://github.com/new/example/releases/download/1.10.0/manifest.json"
	doer := &recordingDoer{responses: map[string]fakeResponse{
		officialRegistryURL:                               {body: `[{"id":"example","name":"Example","author":"A","description":"D","repo":"old/example"}]`},
		"https://api.test/repos/old/example":              {body: `{"full_name":"new/example"}`},
		"https://raw.test/new/example/HEAD/manifest.json": {body: `{"id":"example","name":"Example","version":"1.10.0","minAppVersion":"1.6.0","isDesktopOnly":true}`},
		"https://api.test/repos/new/example/releases/tags/1.10.0": {body: `{"tag_name":"1.10.0","html_url":"https://github.com/new/example/releases/tag/1.10.0","assets":[
			{"name":"manifest.json","browser_download_url":"https://github.com/new/example/releases/download/1.10.0/manifest.json"},
			{"name":"main.js","browser_download_url":"https://github.com/new/example/releases/download/1.10.0/main.js"},
			{"name":"styles.css","browser_download_url":"https://github.com/new/example/releases/download/1.10.0/styles.css"}]}`},
		manifestAsset: {body: `{"id":"example","name":"Example","version":"1.10.0","minAppVersion":"1.6.0","isDesktopOnly":true}`},
	}}

	release, err := newOfficialTestResolver(doer).Resolve(context.Background(), "example", Target{ObsidianVersion: "1.8.0"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if release.PluginID != "example" || release.Repository != "new/example" {
		t.Fatalf("identity = %q, %q", release.PluginID, release.Repository)
	}
	if release.Version != "1.10.0" || release.MinimumObsidianVersion != "1.6.0" || !release.DesktopOnly {
		t.Fatalf("release = %#v", release)
	}
	if release.ReleaseURL != "https://github.com/new/example/releases/tag/1.10.0" || release.Assets.MainJS.URL == "" || release.Assets.Manifest.URL == "" || release.Assets.Styles == nil {
		t.Fatalf("release metadata = %#v", release)
	}
	wantRequests := []string{
		officialRegistryURL,
		"https://api.test/repos/old/example",
		"https://raw.test/new/example/HEAD/manifest.json",
		"https://api.test/repos/new/example/releases/tags/1.10.0",
		manifestAsset,
	}
	if !reflect.DeepEqual(doer.requests, wantRequests) {
		t.Fatalf("requests = %#v, want %#v", doer.requests, wantRequests)
	}
	assertNeverDownloadedPluginCode(t, doer.requests)
}

func TestOfficialResolverSelectsGreatestCompatibleSparseFallback(t *testing.T) {
	t.Parallel()

	doer := &recordingDoer{responses: map[string]fakeResponse{
		officialRegistryURL:                                                      {body: `[{"id":"example","repo":"acme/example"}]`},
		"https://api.test/repos/acme/example":                                    {body: `{"full_name":"acme/example"}`},
		"https://raw.test/acme/example/HEAD/manifest.json":                       {body: `{"id":"example","name":"Example","version":"2.0.0","minAppVersion":"2.0.0"}`},
		"https://raw.test/acme/example/HEAD/versions.json":                       {body: `{"1.4.0":"1.5.0","1.10.0":"1.7.0","1.11.0":"1.9.0"}`},
		"https://api.test/repos/acme/example/releases/tags/1.10.0":               {body: `{"tag_name":"1.10.0","html_url":"https://github.com/acme/example/releases/tag/1.10.0","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/acme/example/releases/download/1.10.0/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/acme/example/releases/download/1.10.0/main.js"}]}`},
		"https://github.com/acme/example/releases/download/1.10.0/manifest.json": {body: `{"id":"example","name":"Example","version":"1.10.0","minAppVersion":"1.7.0"}`},
	}}

	release, err := newOfficialTestResolver(doer).Resolve(context.Background(), "example", Target{ObsidianVersion: "1.8.0"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if release.Version != "1.10.0" || release.MinimumObsidianVersion != "1.7.0" {
		t.Fatalf("selected release = %q requiring %q", release.Version, release.MinimumObsidianVersion)
	}
	wantRequests := []string{
		officialRegistryURL,
		"https://api.test/repos/acme/example",
		"https://raw.test/acme/example/HEAD/manifest.json",
		"https://raw.test/acme/example/HEAD/versions.json",
		"https://api.test/repos/acme/example/releases/tags/1.10.0",
		"https://github.com/acme/example/releases/download/1.10.0/manifest.json",
	}
	if !reflect.DeepEqual(doer.requests, wantRequests) {
		t.Fatalf("requests = %#v, want %#v", doer.requests, wantRequests)
	}
	assertNeverDownloadedPluginCode(t, doer.requests)
}

func TestOfficialResolverRejectsVPrefixedReleaseTag(t *testing.T) {
	t.Parallel()

	doer := &recordingDoer{responses: map[string]fakeResponse{
		officialRegistryURL:                                       {body: `[{"id":"example","repo":"acme/example"}]`},
		"https://api.test/repos/acme/example":                     {body: `{"full_name":"acme/example"}`},
		"https://raw.test/acme/example/HEAD/manifest.json":        {body: `{"id":"example","name":"Example","version":"1.0.0","minAppVersion":"1.0.0"}`},
		"https://api.test/repos/acme/example/releases/tags/1.0.0": {body: `{"tag_name":"v1.0.0","html_url":"https://example.invalid/v1","assets":[]}`},
	}}

	_, err := newOfficialTestResolver(doer).Resolve(context.Background(), "example", Target{ObsidianVersion: "1.8.0"})
	assertSourceError(t, err, ErrorMalformedMetadata, 0)
	assertNeverDownloadedPluginCode(t, doer.requests)
}

func TestOfficialResolverErrorsHaveStableCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		client     HTTPDoer
		wantCode   ErrorCode
		wantStatus int
	}{
		{
			name: "missing official id",
			client: &recordingDoer{responses: map[string]fakeResponse{
				officialRegistryURL: {body: `[{"id":"other","repo":"acme/other"}]`},
			}},
			wantCode: ErrorOfficialPluginNotFound,
		},
		{
			name: "malformed registry",
			client: &recordingDoer{responses: map[string]fakeResponse{
				officialRegistryURL: {body: `{`},
			}},
			wantCode: ErrorMalformedMetadata,
		},
		{
			name: "rate limited",
			client: &recordingDoer{responses: map[string]fakeResponse{
				officialRegistryURL: {status: http.StatusForbidden, header: http.Header{"X-Ratelimit-Remaining": []string{"0"}}},
			}},
			wantCode: ErrorRateLimited, wantStatus: http.StatusForbidden,
		},
		{
			name: "transport failure", client: failingDoer{err: errors.New("network down")}, wantCode: ErrorTransportFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := newOfficialTestResolver(tt.client).Resolve(context.Background(), "missing", Target{ObsidianVersion: "1.8.0"})
			assertSourceError(t, err, tt.wantCode, tt.wantStatus)
		})
	}
}

func TestOfficialResolverRejectsNoCompatibleRelease(t *testing.T) {
	t.Parallel()

	doer := &recordingDoer{responses: map[string]fakeResponse{
		officialRegistryURL:                                {body: `[{"id":"example","repo":"acme/example"}]`},
		"https://api.test/repos/acme/example":              {body: `{"full_name":"acme/example"}`},
		"https://raw.test/acme/example/HEAD/manifest.json": {body: `{"id":"example","name":"Example","version":"2.0.0","minAppVersion":"2.0.0"}`},
		"https://raw.test/acme/example/HEAD/versions.json": {body: `{"2.0.0":"2.0.0"}`},
	}}

	_, err := newOfficialTestResolver(doer).Resolve(context.Background(), "example", Target{ObsidianVersion: "1.8.0"})
	assertSourceError(t, err, ErrorNoCompatibleRelease, 0)
	if len(doer.requests) != 4 {
		t.Fatalf("requests = %#v, want registry, repository, root manifest, and versions only", doer.requests)
	}
}

func TestOfficialResolverRejectsReleaseManifestMismatch(t *testing.T) {
	t.Parallel()

	doer := &recordingDoer{responses: map[string]fakeResponse{
		officialRegistryURL:                                                     {body: `[{"id":"example","repo":"acme/example"}]`},
		"https://api.test/repos/acme/example":                                   {body: `{"full_name":"acme/example"}`},
		"https://raw.test/acme/example/HEAD/manifest.json":                      {body: `{"id":"example","name":"Example","version":"1.0.0","minAppVersion":"1.0.0"}`},
		"https://api.test/repos/acme/example/releases/tags/1.0.0":               {body: `{"tag_name":"1.0.0","html_url":"https://github.com/acme/example/releases/tag/1.0.0","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/acme/example/releases/download/1.0.0/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/acme/example/releases/download/1.0.0/main.js"}]}`},
		"https://github.com/acme/example/releases/download/1.0.0/manifest.json": {body: `{"id":"different","name":"Example","version":"1.0.0","minAppVersion":"1.0.0"}`},
	}}

	_, err := newOfficialTestResolver(doer).Resolve(context.Background(), "example", Target{ObsidianVersion: "1.8.0"})
	assertSourceError(t, err, ErrorMalformedMetadata, 0)
	assertNeverDownloadedPluginCode(t, doer.requests)
}

func TestOfficialResolverLookupReturnsCanonicalIdentityWithoutReleaseRequests(t *testing.T) {
	t.Parallel()

	doer := &recordingDoer{responses: map[string]fakeResponse{
		officialRegistryURL: {body: `[
			{"id":"Example","name":"Wrong Case","author":"Other","description":"Other","repo":"other/example"},
			{"id":"example","name":"Example Plugin","author":"A. Author","description":"Does things","repo":"old/example"}
		]`},
		"https://api.test/repos/old/example": {body: `{"full_name":"new/example"}`},
	}}

	plugin, err := newOfficialTestResolver(doer).Lookup(context.Background(), "example")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if plugin.ID != "example" || plugin.Name != "Example Plugin" || plugin.Author != "A. Author" || plugin.Description != "Does things" || plugin.Repository != "new/example" {
		t.Fatalf("identity = %#v", plugin)
	}
	wantRequests := []string{officialRegistryURL, "https://api.test/repos/old/example"}
	if !reflect.DeepEqual(doer.requests, wantRequests) {
		t.Fatalf("requests = %#v, want %#v", doer.requests, wantRequests)
	}
}

func TestOfficialResolverLookupRequiresExactOfficialID(t *testing.T) {
	t.Parallel()

	doer := &recordingDoer{responses: map[string]fakeResponse{
		officialRegistryURL: {body: `[{"id":"Example","repo":"acme/example"}]`},
	}}
	_, err := newOfficialTestResolver(doer).Lookup(context.Background(), "example")
	assertSourceError(t, err, ErrorOfficialPluginNotFound, 0)
	if len(doer.requests) != 1 {
		t.Fatalf("requests = %#v, want registry only", doer.requests)
	}
}

func TestOfficialResolverRecognizeCachesRegistryWithoutRepositoryRequests(t *testing.T) {
	t.Parallel()

	doer := &recordingDoer{responses: map[string]fakeResponse{
		officialRegistryURL: {body: `[{"id":"example","repo":"acme/example"}]`},
	}}
	resolver := newOfficialTestResolver(doer)

	for _, test := range []struct {
		id   string
		want bool
	}{{id: "example", want: true}, {id: "missing", want: false}, {id: "example", want: true}} {
		got, err := resolver.Recognize(context.Background(), test.id)
		if err != nil {
			t.Fatalf("Recognize(%q) error = %v", test.id, err)
		}
		if got != test.want {
			t.Fatalf("Recognize(%q) = %v, want %v", test.id, got, test.want)
		}
	}

	if !reflect.DeepEqual(doer.requests, []string{officialRegistryURL}) {
		t.Fatalf("requests = %#v, want one registry request and no repository or release requests", doer.requests)
	}
}

func TestOfficialResolverInspectsReleaseWithoutGitHubAPIRequests(t *testing.T) {
	t.Parallel()

	doer := &recordingDoer{responses: map[string]fakeResponse{
		officialRegistryURL: {body: `[{"id":"example","repo":"acme/example"}]`},
		"https://raw.test/acme/example/HEAD/manifest.json":  {body: `{"id":"example","name":"Example","author":"A","description":"D","version":"2.0.0","minAppVersion":"2.0.0"}`},
		"https://raw.test/acme/example/HEAD/versions.json":  {body: `{"1.5.0":"1.7.0","2.0.0":"2.0.0"}`},
		"https://raw.test/acme/example/1.5.0/manifest.json": {body: `{"id":"example","name":"Example","author":"A","description":"D","version":"1.5.0","minAppVersion":"1.7.0","isDesktopOnly":true}`},
	}}

	release, err := newOfficialTestResolver(doer).Inspect(context.Background(), "example", Target{ObsidianVersion: "1.8.0"})
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if release.PluginID != "example" || release.Repository != "acme/example" || release.Version != "1.5.0" || release.MinimumObsidianVersion != "1.7.0" || !release.DesktopOnly {
		t.Fatalf("release = %#v", release)
	}
	if release.ReleaseURL != "https://github.com/acme/example/releases/tag/1.5.0" || release.ReleaseManifest.Name != "Example" {
		t.Fatalf("release metadata = %#v", release)
	}
	wantRequests := []string{
		officialRegistryURL,
		"https://raw.test/acme/example/HEAD/manifest.json",
		"https://raw.test/acme/example/HEAD/versions.json",
		"https://raw.test/acme/example/1.5.0/manifest.json",
	}
	if !reflect.DeepEqual(doer.requests, wantRequests) {
		t.Fatalf("requests = %#v, want no GitHub API or release-asset requests: %#v", doer.requests, wantRequests)
	}
}

func TestOfficialResolverExactSkipsRootAndVerifiesOfficialIdentity(t *testing.T) {
	t.Parallel()

	manifestURL := "https://github.com/new/example/releases/download/1.2.3/manifest.json"
	doer := &recordingDoer{responses: map[string]fakeResponse{
		officialRegistryURL:                                      {body: `[{"id":"example","name":"Example","repo":"old/example"}]`},
		"https://api.test/repos/old/example":                     {body: `{"full_name":"new/example"}`},
		"https://api.test/repos/new/example/releases/tags/1.2.3": {body: `{"tag_name":"1.2.3","html_url":"https://github.com/new/example/releases/tag/1.2.3","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/new/example/releases/download/1.2.3/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/new/example/releases/download/1.2.3/main.js"}]}`},
		manifestURL: {body: `{"id":"example","name":"Example","version":"1.2.3","minAppVersion":"1.5.0"}`},
	}}
	resolver := newOfficialTestResolver(doer)
	plugin, err := resolver.Lookup(context.Background(), "example")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	release, err := resolver.ResolveExact(context.Background(), plugin, "1.2.3", Target{ObsidianVersion: "1.8.0"})
	if err != nil {
		t.Fatalf("ResolveExact() error = %v", err)
	}
	if release.PluginID != "example" || release.Version != "1.2.3" || release.Repository != "new/example" {
		t.Fatalf("release = %#v", release)
	}
	wantRequests := []string{
		officialRegistryURL,
		"https://api.test/repos/old/example",
		"https://api.test/repos/new/example/releases/tags/1.2.3",
		manifestURL,
	}
	if !reflect.DeepEqual(doer.requests, wantRequests) {
		t.Fatalf("requests = %#v, want no root manifest, versions, or release-list request: %#v", doer.requests, wantRequests)
	}
}

func TestOfficialResolverExactRejectsDifferentManifestID(t *testing.T) {
	t.Parallel()

	manifestURL := "https://github.com/acme/example/releases/download/1.2.3/manifest.json"
	doer := &recordingDoer{responses: map[string]fakeResponse{
		"https://api.test/repos/acme/example/releases/tags/1.2.3": {body: `{"tag_name":"1.2.3","html_url":"https://github.com/acme/example/releases/tag/1.2.3","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/acme/example/releases/download/1.2.3/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/acme/example/releases/download/1.2.3/main.js"}]}`},
		manifestURL: {body: `{"id":"other","name":"Other","version":"1.2.3","minAppVersion":"1.5.0"}`},
	}}
	plugin := OfficialPlugin{ID: "example", Repository: "acme/example"}
	_, err := newOfficialTestResolver(doer).ResolveExact(context.Background(), plugin, "1.2.3", Target{ObsidianVersion: "1.8.0"})
	assertSourceError(t, err, ErrorMalformedMetadata, 0)
}

func newOfficialTestResolver(client HTTPDoer) *OfficialResolver {
	return NewOfficialResolver(OfficialConfig{
		Client:           client,
		RegistryURL:      officialRegistryURL,
		RawBaseURL:       "https://raw.test",
		GitHubAPIBaseURL: "https://api.test",
	})
}

func assertSourceError(t *testing.T, err error, wantCode ErrorCode, wantStatus int) {
	t.Helper()
	var sourceErr *Error
	if !errors.As(err, &sourceErr) || sourceErr.Code != wantCode || sourceErr.StatusCode != wantStatus {
		t.Fatalf("error = %T %#v, want code %q status %d", err, sourceErr, wantCode, wantStatus)
	}
}

type failingDoer struct{ err error }

func (d failingDoer) Do(*http.Request) (*http.Response, error) { return nil, d.err }
