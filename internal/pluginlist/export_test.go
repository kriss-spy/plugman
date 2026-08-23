package pluginlist_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kriss-spy/plugman/internal/input"
	"github.com/kriss-spy/plugman/internal/pluginlist"
)

func TestExportWritesDeterministicExactPluginListAcceptedByInputParser(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "vault.plugins")
	plugins := []pluginlist.Plugin{
		{ID: "zotero", Version: "2.3.1", Source: pluginlist.Source{Kind: pluginlist.SourceOfficial}},
		{ID: "opencode", Version: "1.3.13", Source: pluginlist.Source{Kind: pluginlist.SourceGitHub, Repository: "https://github.com/kriss-spy/obsidian-opencode/", Release: "1.3.13"}},
		{ID: "dataview", Version: "0.5.67", Source: pluginlist.Source{Kind: pluginlist.SourceOfficial}},
	}

	result, err := pluginlist.Export(path, plugins, pluginlist.Options{})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "dataview@0.5.67\nhttps://github.com/kriss-spy/obsidian-opencode/releases/tag/1.3.13\nzotero@2.3.1\n"
	if string(contents) != want || result.Written != 3 || result.Path != path {
		t.Fatalf("Export() = %#v, contents = %q; want %q", result, contents, want)
	}
	declarations, err := input.Expand(dir, []string{path})
	if err != nil || len(declarations) != 3 {
		t.Fatalf("input.Expand(export) = %#v, %v", declarations, err)
	}
}

func TestExportEnabledLatestUsesUnversionedReusableDeclarations(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "enabled.plugins")
	plugins := []pluginlist.Plugin{
		{ID: "disabled", Version: "1.0.0", Source: pluginlist.Source{Kind: pluginlist.SourceOfficial}},
		{ID: "dataview", Version: "0.5.67", Enabled: true, Source: pluginlist.Source{Kind: pluginlist.SourceOfficial}},
		{ID: "opencode", Version: "1.3.13", Enabled: true, Source: pluginlist.Source{Kind: pluginlist.SourceGitHub, Repository: "https://github.com/kriss-spy/obsidian-opencode", Release: "1.3.13"}},
	}

	_, err := pluginlist.Export(path, plugins, pluginlist.Options{EnabledOnly: true, Latest: true})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "dataview\nhttps://github.com/kriss-spy/obsidian-opencode\n"
	if string(contents) != want {
		t.Fatalf("contents = %q, want %q", contents, want)
	}
	if declarations, err := input.Expand(dir, []string{path}); err != nil || len(declarations) != 2 {
		t.Fatalf("input.Expand(export) = %#v, %v", declarations, err)
	}
}

func TestExportFailsValidationBeforeWritingAnything(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "invalid.plugins")
	plugins := []pluginlist.Plugin{
		{ID: "dataview", Version: "0.5.67", Source: pluginlist.Source{Kind: pluginlist.SourceOfficial}},
		{ID: "Bad ID", Version: "not-a-version", Source: pluginlist.Source{Kind: pluginlist.SourceOfficial}},
	}

	_, err := pluginlist.Export(path, plugins, pluginlist.Options{})
	var exportError *pluginlist.Error
	if !errors.As(err, &exportError) || exportError.Code != pluginlist.InvalidPlugin {
		t.Fatalf("Export() error = %v, want invalid-plugin error", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination was created before validation completed: %v", statErr)
	}
}

func TestExportRefusesOverwriteUnlessForced(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "existing.plugins")
	if err := os.WriteFile(path, []byte("keep-me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plugins := []pluginlist.Plugin{{ID: "dataview", Version: "0.5.67", Source: pluginlist.Source{Kind: pluginlist.SourceOfficial}}}

	_, err := pluginlist.Export(path, plugins, pluginlist.Options{})
	var exportError *pluginlist.Error
	if !errors.As(err, &exportError) || exportError.Code != pluginlist.DestinationExists {
		t.Fatalf("Export() error = %v, want destination-exists", err)
	}
	contents, readErr := os.ReadFile(path)
	if readErr != nil || string(contents) != "keep-me\n" {
		t.Fatalf("refused export changed destination: contents=%q err=%v", contents, readErr)
	}

	result, err := pluginlist.Export(path, plugins, pluginlist.Options{Force: true})
	if err != nil || result.Written != 1 {
		t.Fatalf("forced Export() = %#v, %v", result, err)
	}
	contents, readErr = os.ReadFile(path)
	if readErr != nil || string(contents) != "dataview@0.5.67\n" {
		t.Fatalf("forced export contents=%q err=%v", contents, readErr)
	}
}

func TestExportReportsEveryIncludedUnresolvedPluginWithoutWriting(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "unknown.plugins")
	plugins := []pluginlist.Plugin{{ID: "local-plugin", Version: "1.0.0", Enabled: true, Source: pluginlist.Source{Kind: pluginlist.SourceUnknown}}}

	_, err := pluginlist.Export(path, plugins, pluginlist.Options{})
	var exportError *pluginlist.Error
	if !errors.As(err, &exportError) || exportError.Code != pluginlist.UnresolvedPlugin || exportError.PluginID != "local-plugin" {
		t.Fatalf("Export() error = %#v, want unresolved local-plugin", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination was created: %v", statErr)
	}
}

func TestExportRejectsNonRegularForceDestinationWithoutChangingIt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "existing.plugins")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	plugins := []pluginlist.Plugin{{ID: "dataview", Version: "0.5.67", Source: pluginlist.Source{Kind: pluginlist.SourceOfficial}}}

	_, err := pluginlist.Export(path, plugins, pluginlist.Options{Force: true})
	var exportError *pluginlist.Error
	if !errors.As(err, &exportError) || exportError.Code != pluginlist.DestinationFailure {
		t.Fatalf("Export() error = %#v, want destination-failure", err)
	}
	info, statErr := os.Stat(path)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("force export changed non-regular destination: info=%#v err=%v", info, statErr)
	}
}

func TestExportRejectsGitHubReleaseThatCannotRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "invalid-release.plugins")
	plugins := []pluginlist.Plugin{{
		ID: "opencode", Version: "1.3.13",
		Source: pluginlist.Source{Kind: pluginlist.SourceGitHub, Repository: "https://github.com/kriss-spy/obsidian-opencode", Release: "feature/release"},
	}}

	_, err := pluginlist.Export(path, plugins, pluginlist.Options{})
	var exportError *pluginlist.Error
	if !errors.As(err, &exportError) || exportError.Code != pluginlist.InvalidPlugin {
		t.Fatalf("Export() error = %#v, want invalid-plugin", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination was created: %v", statErr)
	}
}
