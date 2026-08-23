package source

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestObsidianVersionProviderReadsStableDesktopVersion(t *testing.T) {
	provider := NewObsidianVersionProvider(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"latestVersion":"1.13.7"}`)), Header: make(http.Header)}, nil
	}), "https://example.test/desktop-releases.json")

	got, err := provider.Latest(context.Background())
	if err != nil || got != "1.13.7" {
		t.Fatalf("Latest() = %q, %v", got, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }
