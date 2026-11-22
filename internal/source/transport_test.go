package source

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

func TestGitHubResolutionRecoversTransientTransportFailure(t *testing.T) {
	for _, endpoint := range []string{
		"https://api.test/repos/acme/plugin",
		"https://github.com/acme/plugin/releases/download/1.0.0/manifest.json",
	} {
		t.Run(endpoint, func(t *testing.T) {
			fixture := &recordingDoer{responses: map[string]fakeResponse{
				"https://api.test/repos/acme/plugin":                                   {body: `{"full_name":"acme/plugin"}`},
				"https://api.test/repos/acme/plugin/releases/tags/1.0.0":               {body: `{"tag_name":"1.0.0","html_url":"https://github.com/acme/plugin/releases/tag/1.0.0","assets":[{"name":"manifest.json","browser_download_url":"https://github.com/acme/plugin/releases/download/1.0.0/manifest.json"},{"name":"main.js","browser_download_url":"https://github.com/acme/plugin/releases/download/1.0.0/main.js"}]}`},
				"https://github.com/acme/plugin/releases/download/1.0.0/manifest.json": {body: `{"id":"plugin","name":"Plugin","version":"1.0.0"}`},
			}}
			attempts := 0
			client := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() == endpoint {
					attempts++
					if attempts == 1 {
						return nil, &net.DNSError{IsTimeout: true}
					}
				}
				return fixture.Do(request)
			})
			release, _, err := newGitHubTestResolver(client).Resolve(context.Background(), "https://github.com/acme/plugin/releases/tag/1.0.0", Target{ObsidianVersion: "1.15.1"})
			if err != nil || release.Version != "1.0.0" || attempts != 2 {
				t.Fatalf("release=%+v attempts=%d error=%v", release, attempts, err)
			}
		})
	}
}

func TestTransportFailureReportsSafeCauseAndStopsRetrying(t *testing.T) {
	const secret = "transport-secret"
	t.Setenv("GH_TOKEN", secret)
	for _, test := range []struct {
		name     string
		cause    error
		want     string
		attempts int
	}{
		{"timeout", &net.DNSError{IsTimeout: true, Name: secret}, "request timed out", 3},
		{"DNS", &net.DNSError{IsNotFound: true, Name: secret}, "DNS lookup failed", 1},
		{"connection", io.EOF, "connection closed", 3},
		{"reset", syscall.ECONNRESET, "connection closed", 3},
		{"TLS", x509.UnknownAuthorityError{}, "TLS certificate validation failed", 1},
		{"unknown", errors.New(secret), "check network and proxy settings", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			client := roundTripFunc(func(*http.Request) (*http.Response, error) {
				attempts++
				return nil, &url.Error{Op: "Get", URL: "https://user:" + secret + "@proxy.test/?token=" + secret, Err: test.cause}
			})
			_, _, err := newGitHubTestResolver(client).Resolve(context.Background(), "https://github.com/acme/plugin", Target{ObsidianVersion: "1.15.1"})
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), secret) || attempts != test.attempts {
				t.Fatalf("attempts=%d error=%v", attempts, err)
			}
		})
	}
}

func TestTransportRetryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	client := roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		cancel()
		return nil, io.EOF
	})
	_, _, err := newGitHubTestResolver(client).Resolve(ctx, "https://github.com/acme/plugin", Target{ObsidianVersion: "1.15.1"})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("attempts=%d error=%v", attempts, err)
	}
}
