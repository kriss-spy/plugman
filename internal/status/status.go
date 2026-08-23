// Package status computes read-only update status from installed plugin facts.
package status

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
)

// ResolveErrorKind identifies expected negative resolution outcomes.
type ResolveErrorKind string

const (
	ResolveRemoved      ResolveErrorKind = "removed"
	ResolveIncompatible ResolveErrorKind = "incompatible"
)

// ResolveError lets adapters distinguish removal and compatibility from
// ordinary transport failures.
type ResolveError struct {
	Kind    ResolveErrorKind
	Message string
	Err     error
}

func (e *ResolveError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Kind)
}

func (e *ResolveError) Unwrap() error { return e.Err }

// SourceKind identifies how Plugman can resolve an installed plugin.
type SourceKind string

const (
	SourceOfficial SourceKind = "official"
	SourceGitHub   SourceKind = "github"
	SourceUnknown  SourceKind = "unknown"
)

// State is the stable outcome of checking one installed plugin.
type State string

const (
	Current       State = "current"
	Outdated      State = "outdated"
	UnknownSource State = "unknown-source"
	Removed       State = "removed"
	Incompatible  State = "incompatible"
	Transport     State = "transport"
)

// Source preserves the locally known origin of an installed plugin.
type Source struct {
	Kind       SourceKind
	Repository string
	Release    string
}

// Installed is the local state needed by an outdated check.
type Installed struct {
	ID      string
	Version string
	Enabled bool
	Source  Source
}

// Release is the newest release selected for the current compatibility target.
type Release struct {
	Version string
	URL     string
}

// Resolver resolves the newest compatible release for a known source.
type Resolver interface {
	ResolveLatest(context.Context, Installed) (Release, error)
}

// Result preserves local facts and adds the outcome of remote resolution.
type Result struct {
	ID             string
	CurrentVersion string
	LatestVersion  string
	Enabled        bool
	Source         Source
	ReleaseURL     string
	State          State
	Problem        string
}

// Check resolves and classifies each plugin in the supplied deterministic order.
func Check(ctx context.Context, plugins []Installed, resolver Resolver) []Result {
	results := make([]Result, len(plugins))
	if len(plugins) == 0 {
		return results
	}
	type job struct {
		index  int
		plugin Installed
	}
	jobs := make(chan job)
	workerCount := min(len(plugins), 6)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for item := range jobs {
				results[item.index] = checkOne(ctx, item.plugin, resolver)
			}
		}()
	}
	for index, plugin := range plugins {
		jobs <- job{index: index, plugin: plugin}
	}
	close(jobs)
	workers.Wait()
	return results
}

func checkOne(ctx context.Context, plugin Installed, resolver Resolver) Result {
	result := Result{ID: plugin.ID, CurrentVersion: plugin.Version, Enabled: plugin.Enabled, Source: plugin.Source}
	if plugin.Source.Kind != SourceOfficial && plugin.Source.Kind != SourceGitHub {
		result.State = UnknownSource
		return result
	}
	release, err := resolver.ResolveLatest(ctx, plugin)
	if err != nil {
		result.State = Transport
		var resolveError *ResolveError
		if errors.As(err, &resolveError) {
			switch resolveError.Kind {
			case ResolveRemoved:
				result.State = Removed
			case ResolveIncompatible:
				result.State = Incompatible
			}
		}
		result.Problem = err.Error()
		return result
	}
	result.LatestVersion = release.Version
	result.ReleaseURL = release.URL
	if compareVersions(release.Version, plugin.Version) > 0 {
		result.State = Outdated
	} else {
		result.State = Current
	}
	return result
}

func compareVersions(left, right string) int {
	lversion, lok := parseVersion(left)
	rversion, rok := parseVersion(right)
	if !lok || !rok {
		return strings.Compare(left, right)
	}
	for i := 0; i < 3; i++ {
		if lversion.numbers[i] < rversion.numbers[i] {
			return -1
		}
		if lversion.numbers[i] > rversion.numbers[i] {
			return 1
		}
	}
	if len(lversion.prerelease) == 0 && len(rversion.prerelease) > 0 {
		return 1
	}
	if len(lversion.prerelease) > 0 && len(rversion.prerelease) == 0 {
		return -1
	}
	for i := 0; i < len(lversion.prerelease) && i < len(rversion.prerelease); i++ {
		leftID, rightID := lversion.prerelease[i], rversion.prerelease[i]
		leftNumber, leftNumeric := numericIdentifier(leftID)
		rightNumber, rightNumeric := numericIdentifier(rightID)
		switch {
		case leftNumeric && rightNumeric && leftNumber < rightNumber:
			return -1
		case leftNumeric && rightNumeric && leftNumber > rightNumber:
			return 1
		case leftNumeric && !rightNumeric:
			return -1
		case !leftNumeric && rightNumeric:
			return 1
		case leftID < rightID:
			return -1
		case leftID > rightID:
			return 1
		}
	}
	if len(lversion.prerelease) < len(rversion.prerelease) {
		return -1
	}
	if len(lversion.prerelease) > len(rversion.prerelease) {
		return 1
	}
	return 0
}

type version struct {
	numbers    [3]int
	prerelease []string
}

func parseVersion(value string) (version, bool) {
	value = strings.TrimPrefix(value, "v")
	value = strings.SplitN(value, "+", 2)[0]
	parts := strings.SplitN(value, "-", 2)
	numbers := strings.Split(parts[0], ".")
	if len(numbers) != 3 {
		return version{}, false
	}
	parsed := version{}
	for i, number := range numbers {
		if number == "" {
			return version{}, false
		}
		parsed.numbers[i], _ = strconv.Atoi(number)
		if strconv.Itoa(parsed.numbers[i]) != number {
			return version{}, false
		}
	}
	if len(parts) == 2 {
		if parts[1] == "" {
			return version{}, false
		}
		parsed.prerelease = strings.Split(parts[1], ".")
	}
	return parsed, true
}

func numericIdentifier(value string) (int, bool) {
	number, err := strconv.Atoi(value)
	return number, err == nil && strconv.Itoa(number) == value
}
