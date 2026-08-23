package status_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kriss-spy/plugman/internal/status"
)

func TestCheckResolvesPluginsConcurrentlyAndPreservesOrder(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	resolver := resolverFunc(func(_ context.Context, plugin status.Installed) (status.Release, error) {
		started <- plugin.ID
		<-release
		return status.Release{Version: "2.0.0"}, nil
	})
	plugins := []status.Installed{
		{ID: "first", Version: "1.0.0", Source: status.Source{Kind: status.SourceOfficial}},
		{ID: "second", Version: "1.0.0", Source: status.Source{Kind: status.SourceOfficial}},
		{ID: "third", Version: "1.0.0", Source: status.Source{Kind: status.SourceOfficial}},
	}
	done := make(chan []status.Result, 1)
	go func() { done <- status.Check(context.Background(), plugins, resolver) }()

	for range plugins {
		select {
		case <-started:
		case <-time.After(500 * time.Millisecond):
			close(release)
			t.Fatal("plugin checks ran one-by-one")
		}
	}
	close(release)
	got := <-done
	for index, plugin := range plugins {
		if got[index].ID != plugin.ID {
			t.Fatalf("result %d ID = %q, want %q", index, got[index].ID, plugin.ID)
		}
	}
}

type resolverFunc func(context.Context, status.Installed) (status.Release, error)

func (f resolverFunc) ResolveLatest(ctx context.Context, plugin status.Installed) (status.Release, error) {
	return f(ctx, plugin)
}

func TestCheckClassifiesUnknownRemovedIncompatibleAndTransportStates(t *testing.T) {
	t.Parallel()

	resolver := resolverFunc(func(_ context.Context, plugin status.Installed) (status.Release, error) {
		switch plugin.ID {
		case "removed":
			return status.Release{}, &status.ResolveError{Kind: status.ResolveRemoved, Message: "not in directory"}
		case "incompatible":
			return status.Release{}, &status.ResolveError{Kind: status.ResolveIncompatible, Message: "no compatible release"}
		default:
			return status.Release{}, errors.New("network unavailable")
		}
	})
	plugins := []status.Installed{
		{ID: "local", Version: "1.0.0", Source: status.Source{Kind: status.SourceUnknown}},
		{ID: "removed", Version: "1.0.0", Source: status.Source{Kind: status.SourceOfficial}},
		{ID: "incompatible", Version: "1.0.0", Source: status.Source{Kind: status.SourceOfficial}},
		{ID: "offline", Version: "1.0.0", Source: status.Source{Kind: status.SourceGitHub}},
	}

	got := status.Check(context.Background(), plugins, resolver)
	want := []status.State{status.UnknownSource, status.Removed, status.Incompatible, status.Transport}
	for i := range want {
		if got[i].State != want[i] {
			t.Errorf("result %d state = %q, want %q", i, got[i].State, want[i])
		}
	}
	if got[0].Problem != "" || got[1].Problem == "" || got[2].Problem == "" || got[3].Problem == "" {
		t.Fatalf("problem details = %#v", got)
	}
}

func TestCheckUsesSemanticVersionPrecedence(t *testing.T) {
	t.Parallel()

	latest := map[string]string{
		"prerelease": "1.0.0-beta.10",
		"build":      "1.0.0+different-build",
	}
	resolver := resolverFunc(func(_ context.Context, plugin status.Installed) (status.Release, error) {
		return status.Release{Version: latest[plugin.ID]}, nil
	})
	plugins := []status.Installed{
		{ID: "prerelease", Version: "1.0.0-beta.2", Source: status.Source{Kind: status.SourceOfficial}},
		{ID: "build", Version: "1.0.0+local-build", Source: status.Source{Kind: status.SourceOfficial}},
	}

	got := status.Check(context.Background(), plugins, resolver)
	if got[0].State != status.Outdated || got[1].State != status.Current {
		t.Fatalf("states = %q, %q; want outdated, current", got[0].State, got[1].State)
	}
}

func TestCheckClassifiesCurrentAndOutdatedPlugins(t *testing.T) {
	t.Parallel()

	resolver := resolverFunc(func(_ context.Context, plugin status.Installed) (status.Release, error) {
		return status.Release{Version: "2.0.0", URL: "https://github.com/owner/repo/releases/tag/2.0.0"}, nil
	})
	plugins := []status.Installed{
		{ID: "current", Version: "2.0.0", Enabled: true, Source: status.Source{Kind: status.SourceOfficial}},
		{ID: "old", Version: "1.0.0", Enabled: false, Source: status.Source{Kind: status.SourceGitHub, Repository: "https://github.com/owner/repo", Release: "1.0.0"}},
	}

	got := status.Check(context.Background(), plugins, resolver)
	if len(got) != 2 {
		t.Fatalf("len(Check()) = %d, want 2", len(got))
	}
	if got[0].State != status.Current || got[1].State != status.Outdated {
		t.Fatalf("states = %q, %q; want current, outdated", got[0].State, got[1].State)
	}
	if !got[0].Enabled || got[1].Enabled || got[1].Source.Kind != status.SourceGitHub || got[1].CurrentVersion != "1.0.0" || got[1].LatestVersion != "2.0.0" || got[1].ReleaseURL == "" {
		t.Fatalf("Check() did not preserve plugin/release facts: %#v", got)
	}
}
