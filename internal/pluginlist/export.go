// Package pluginlist renders installed plugin state as reusable Plugin Lists.
package pluginlist

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	officialIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	semverPattern     = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	githubPartPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

// SourceKind identifies the declaration form used for an exported plugin.
type SourceKind string

const (
	SourceOfficial SourceKind = "official"
	SourceGitHub   SourceKind = "github"
	SourceUnknown  SourceKind = "unknown"
)

// Source contains official or GitHub provenance known for a plugin.
type Source struct {
	Kind       SourceKind
	Repository string
	Release    string
}

// Plugin is installed state eligible for export.
type Plugin struct {
	ID       string
	Version  string
	Enabled  bool
	Source   Source
	Problems []string
}

// Options selects which reusable declaration style Export writes.
type Options struct {
	EnabledOnly bool
	Latest      bool
	Force       bool
}

// Result describes a completed export.
type Result struct {
	Path    string
	Written int
}

// ErrorCode is a stable export failure category.
type ErrorCode string

const (
	InvalidPlugin      ErrorCode = "invalid_plugin"
	UnresolvedPlugin   ErrorCode = "unresolved_plugin"
	DestinationExists  ErrorCode = "destination_exists"
	DestinationFailure ErrorCode = "destination_failure"
)

// Error describes an export failure without tying callers to prose.
type Error struct {
	Code     ErrorCode
	PluginID string
	Path     string
	Message  string
	Err      error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error { return e.Err }

// Export validates the entire selection before writing a deterministic list.
func Export(path string, plugins []Plugin, options Options) (Result, error) {
	lines := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		if options.EnabledOnly && !plugin.Enabled {
			continue
		}
		line, err := declaration(plugin, options.Latest)
		if err != nil {
			return Result{}, err
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	contents := []byte(strings.Join(lines, ""))
	if len(lines) > 0 {
		contents = []byte(strings.Join(lines, "\n") + "\n")
	}
	if err := write(path, contents, options.Force); err != nil {
		return Result{}, err
	}
	return Result{Path: path, Written: len(lines)}, nil
}

func declaration(plugin Plugin, latest bool) (string, error) {
	if !officialIDPattern.MatchString(plugin.ID) || !semverPattern.MatchString(plugin.Version) || len(plugin.Problems) != 0 {
		return "", &Error{Code: InvalidPlugin, PluginID: plugin.ID, Message: fmt.Sprintf("plugin %q has invalid installed state", plugin.ID)}
	}
	switch plugin.Source.Kind {
	case SourceOfficial:
		if latest {
			return plugin.ID, nil
		}
		return plugin.ID + "@" + plugin.Version, nil
	case SourceGitHub:
		if !validRepository(plugin.Source.Repository) || plugin.Source.Release == "" || strings.Contains(plugin.Source.Release, "/") {
			return "", &Error{Code: InvalidPlugin, PluginID: plugin.ID, Message: fmt.Sprintf("plugin %q has an invalid GitHub Source Record", plugin.ID)}
		}
		repository := strings.TrimRight(plugin.Source.Repository, "/")
		if latest {
			return repository, nil
		}
		return repository + "/releases/tag/" + url.PathEscape(plugin.Source.Release), nil
	default:
		return "", &Error{Code: UnresolvedPlugin, PluginID: plugin.ID, Message: fmt.Sprintf("plugin %q is neither official nor accompanied by a Source Record", plugin.ID)}
	}
}

func validRepository(repository string) bool {
	u, err := url.Parse(repository)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) != 2 {
		return false
	}
	owner, ownerErr := url.PathUnescape(parts[0])
	repositoryName, repositoryErr := url.PathUnescape(parts[1])
	return ownerErr == nil && repositoryErr == nil && githubPartPattern.MatchString(owner) && githubPartPattern.MatchString(repositoryName) && owner != "." && owner != ".." && repositoryName != "." && repositoryName != ".."
}

func write(path string, contents []byte, force bool) error {
	if !force {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return &Error{Code: DestinationExists, Path: path, Message: "export destination already exists", Err: err}
			}
			return &Error{Code: DestinationFailure, Path: path, Message: "write export destination", Err: err}
		}
		if _, err := file.Write(contents); err != nil {
			_ = file.Close()
			return &Error{Code: DestinationFailure, Path: path, Message: "write export destination", Err: err}
		}
		if err := file.Close(); err != nil {
			return &Error{Code: DestinationFailure, Path: path, Message: "write export destination", Err: err}
		}
		return nil
	}

	directory := filepath.Dir(path)
	destination, statErr := os.Lstat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return &Error{Code: DestinationFailure, Path: path, Message: "inspect export destination", Err: statErr}
	}
	if statErr == nil && !destination.Mode().IsRegular() {
		return &Error{Code: DestinationFailure, Path: path, Message: "export destination must be a regular file"}
	}
	temporary, err := os.CreateTemp(directory, ".plugman-export-")
	if err != nil {
		return &Error{Code: DestinationFailure, Path: path, Message: "create temporary export", Err: err}
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return &Error{Code: DestinationFailure, Path: path, Message: "prepare temporary export", Err: err}
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return &Error{Code: DestinationFailure, Path: path, Message: "write temporary export", Err: err}
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return &Error{Code: DestinationFailure, Path: path, Message: "flush temporary export", Err: err}
	}
	if err := temporary.Close(); err != nil {
		return &Error{Code: DestinationFailure, Path: path, Message: "write temporary export", Err: err}
	}
	if errors.Is(statErr, os.ErrNotExist) {
		if err := os.Rename(temporaryPath, path); err != nil {
			return &Error{Code: DestinationFailure, Path: path, Message: "install export destination", Err: err}
		}
		return nil
	}

	backupFile, err := os.CreateTemp(directory, ".plugman-export-backup-")
	if err != nil {
		return &Error{Code: DestinationFailure, Path: path, Message: "reserve export backup", Err: err}
	}
	backupPath := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		_ = os.Remove(backupPath)
		return &Error{Code: DestinationFailure, Path: path, Message: "reserve export backup", Err: err}
	}
	if err := os.Remove(backupPath); err != nil {
		return &Error{Code: DestinationFailure, Path: path, Message: "reserve export backup", Err: err}
	}
	if err := os.Rename(path, backupPath); err != nil {
		return &Error{Code: DestinationFailure, Path: path, Message: "preserve existing export", Err: err}
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		restoreErr := os.Rename(backupPath, path)
		if restoreErr != nil {
			return &Error{Code: DestinationFailure, Path: path, Message: fmt.Sprintf("replace export destination: %v; restore previous export: %v", err, restoreErr), Err: err}
		}
		return &Error{Code: DestinationFailure, Path: path, Message: "replace export destination", Err: err}
	}
	_ = os.Remove(backupPath)
	return nil
}
