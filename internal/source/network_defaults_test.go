package source

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOfficialResolverUsesGHTokenOnlyForGitHubMetadata(t *testing.T) {
	t.Setenv("GH_TOKEN", "gh-primary-token")
	t.Setenv("GITHUB_TOKEN", "github-fallback-token")

	var registryAuthorization string
	var githubAuthorization string
	previousTransport := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		switch request.URL.String() {
		case "https://registry.test/community-plugins.json":
			registryAuthorization = request.Header.Get("Authorization")
			body = `[{"id":"example","repo":"acme/example"}]`
		case "https://api.test/repos/acme/example":
			githubAuthorization = request.Header.Get("Authorization")
			body = `{"full_name":"acme/example"}`
		default:
			return nil, &unexpectedRequestError{url: request.URL.String()}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previousTransport })

	resolver := NewOfficialResolver(OfficialConfig{
		RegistryURL:      "https://registry.test/community-plugins.json",
		GitHubAPIBaseURL: "https://api.test",
	})
	if _, err := resolver.Lookup(context.Background(), "example"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if registryAuthorization != "" {
		t.Fatalf("registry Authorization = %q, want empty", registryAuthorization)
	}
	if githubAuthorization != "Bearer gh-primary-token" {
		t.Fatalf("GitHub Authorization = %q, want GH_TOKEN", githubAuthorization)
	}
}

func TestGitHubResolverFallsBackToGitHubTokenForMetadataOnly(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "github-fallback-token")

	authorizations := make(map[string]string)
	previousTransport := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		authorizations[request.URL.String()] = request.Header.Get("Authorization")
		var body string
		switch request.URL.String() {
		case "https://api.test/repos/acme/example":
			body = `{"full_name":"acme/example"}`
		case "https://api.test/repos/acme/example/releases/tags/1.0.0":
			body = `{"tag_name":"1.0.0","html_url":"https://github.com/acme/example/releases/tag/1.0.0","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/acme/example/releases/download/1.0.0/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/acme/example/releases/download/1.0.0/main.js"}]}`
		case "https://github.com/acme/example/releases/download/1.0.0/manifest.json":
			body = `{"id":"example","name":"Example","version":"1.0.0","minAppVersion":"1.0.0"}`
		default:
			return nil, &unexpectedRequestError{url: request.URL.String()}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previousTransport })

	resolver := NewGitHubResolver(GitHubConfig{APIBaseURL: "https://api.test"})
	_, _, err := resolver.Resolve(context.Background(), "https://github.com/acme/example/releases/tag/1.0.0", Target{ObsidianVersion: "1.0.0"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got := authorizations["https://api.test/repos/acme/example"]; got != "Bearer github-fallback-token" {
		t.Fatalf("repository Authorization = %q, want GITHUB_TOKEN", got)
	}
	if got := authorizations["https://api.test/repos/acme/example/releases/tags/1.0.0"]; got != "Bearer github-fallback-token" {
		t.Fatalf("release Authorization = %q, want GITHUB_TOKEN", got)
	}
	if got := authorizations["https://github.com/acme/example/releases/download/1.0.0/manifest.json"]; got != "" {
		t.Fatalf("release manifest Authorization = %q, want empty", got)
	}
}

func TestDefaultSourceClientsHaveFiniteTimeout(t *testing.T) {
	official := NewOfficialResolver(OfficialConfig{})
	github := NewGitHubResolver(GitHubConfig{})
	target := NewObsidianVersionProvider(nil, "")

	for name, doer := range map[string]HTTPDoer{
		"official": official.client,
		"github":   github.client,
		"target":   target.client,
	} {
		client, ok := doer.(*http.Client)
		if !ok {
			t.Fatalf("%s default client has type %T, want *http.Client", name, doer)
		}
		if client.Timeout <= 0 {
			t.Errorf("%s default client timeout = %s, want finite timeout", name, client.Timeout)
		}
	}
}

func TestGitHubTokenIsNotExposedInResolverErrors(t *testing.T) {
	const token = "secret-token-that-must-not-leak"
	t.Setenv("GH_TOKEN", token)
	t.Setenv("GITHUB_TOKEN", "")

	previousTransport := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"X-Ratelimit-Remaining": []string{"0"}},
			Body:       io.NopCloser(strings.NewReader(`{"message":"rate limited"}`)),
			Request:    request,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previousTransport })

	resolver := NewGitHubResolver(GitHubConfig{APIBaseURL: "https://api.test"})
	_, _, err := resolver.Resolve(context.Background(), "https://github.com/acme/example", Target{ObsidianVersion: "1.0.0"})
	if err == nil {
		t.Fatal("Resolve() error = nil, want rate-limit error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("Resolve() error exposes token: %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type unexpectedRequestError struct{ url string }

func (e *unexpectedRequestError) Error() string { return "unexpected request: " + e.url }
