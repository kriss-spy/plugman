// Package input classifies command-line Plugin Inputs and expands Plugin Lists.
package input

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Kind identifies the syntactic source represented by a Plugin Declaration.
type Kind string

const (
	Official         Kind = "official"
	GitHubRepository Kind = "github_repository"
	GitHubRelease    Kind = "github_release"
)

// Declaration is a syntactically valid request ready for source resolution.
// ID is populated for official declarations. Repository and, for exact GitHub
// releases, Release are populated for GitHub declarations.
type Declaration struct {
	Kind       Kind
	ID         string
	Version    string
	Repository string
	Release    string
	Origin     Origin
}

// Origin identifies where a declaration came from. Path and Line are set for
// declarations read from a Plugin List.
type Origin struct {
	Input string
	Path  string
	Line  int
}

// ErrorCode is a stable, machine-readable Plugin Input error category.
type ErrorCode string

const (
	MissingFile             ErrorCode = "missing_file"
	UnreadableList          ErrorCode = "unreadable_plugin_list"
	InvalidDeclaration      ErrorCode = "invalid_declaration"
	ConflictingExactVersion ErrorCode = "conflicting_exact_version"
)

// Problem describes one invalid Plugin Input without tying callers to prose.
type Problem struct {
	Code    ErrorCode
	Origin  Origin
	Message string
}

// ValidationError reports every syntactic input problem discovered in a batch.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return fmt.Sprintf("invalid Plugin Input: %s", e.Problems[0].Message)
	}
	return fmt.Sprintf("invalid Plugin Inputs: %d problems", len(e.Problems))
}

var (
	officialIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	semverPattern     = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	githubPartPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

// Expand classifies inputs relative to baseDir and expands every Plugin List.
// It performs no network access and never writes a Plugin List.
func Expand(baseDir string, inputs []string) ([]Declaration, error) {
	var declarations []Declaration
	var problems []Problem

	for _, raw := range inputs {
		if strings.Contains(raw, "://") {
			declaration, parseProblem := parse(raw, Origin{Input: raw})
			if parseProblem != nil {
				problems = append(problems, *parseProblem)
			} else {
				declarations = append(declarations, declaration)
			}
			continue
		}
		path := raw
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		info, statErr := os.Stat(path)
		switch {
		case statErr == nil:
			if info.IsDir() {
				problems = append(problems, problem(UnreadableList, Origin{Input: raw, Path: path}, "Plugin List is a directory"))
				continue
			}
			fromList, listProblems := readList(path)
			declarations = append(declarations, fromList...)
			problems = append(problems, listProblems...)
			continue
		case !errors.Is(statErr, os.ErrNotExist):
			problems = append(problems, problem(UnreadableList, Origin{Input: raw, Path: path}, "cannot inspect Plugin List"))
			continue
		case fileLike(raw):
			problems = append(problems, problem(MissingFile, Origin{Input: raw, Path: path}, "Plugin List does not exist"))
			continue
		}

		declaration, parseProblem := parse(raw, Origin{Input: raw})
		if parseProblem != nil {
			problems = append(problems, *parseProblem)
			continue
		}
		declarations = append(declarations, declaration)
	}

	deduplicated, conflictProblems := deduplicate(declarations)
	problems = append(problems, conflictProblems...)
	if len(problems) != 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return deduplicated, nil
}

func readList(path string) ([]Declaration, []Problem) {
	file, err := os.Open(path)
	if err != nil {
		return nil, []Problem{problem(UnreadableList, Origin{Input: path, Path: path}, "cannot read Plugin List")}
	}
	defer file.Close()

	var declarations []Declaration
	var problems []Problem
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			line = strings.TrimSpace(line[:comment])
		}
		if line == "" {
			continue
		}
		origin := Origin{Input: line, Path: path, Line: lineNumber}
		declaration, parseProblem := parse(line, origin)
		if parseProblem != nil {
			problems = append(problems, *parseProblem)
			continue
		}
		declarations = append(declarations, declaration)
	}
	if err := scanner.Err(); err != nil {
		problems = append(problems, problem(UnreadableList, Origin{Input: path, Path: path}, "cannot read Plugin List"))
	}
	return declarations, problems
}

func parse(raw string, origin Origin) (Declaration, *Problem) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "https://") || strings.Contains(raw, "://") {
		declaration, ok := parseGitHub(raw, origin)
		if !ok {
			invalid := problem(InvalidDeclaration, origin, "unsupported GitHub URL")
			return Declaration{}, &invalid
		}
		return declaration, nil
	}

	id := raw
	version := ""
	if at := strings.LastIndexByte(raw, '@'); at >= 0 {
		id, version = raw[:at], raw[at+1:]
	}
	if !officialIDPattern.MatchString(id) || (version != "" && !semverPattern.MatchString(version)) || strings.HasSuffix(raw, "@") {
		invalid := problem(InvalidDeclaration, origin, "expected a plugin ID with an optional exact semantic version")
		return Declaration{}, &invalid
	}
	return Declaration{Kind: Official, ID: id, Version: version, Origin: origin}, nil
}

func parseGitHub(raw string, origin Origin) (Declaration, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return Declaration{}, false
	}
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) != 2 && len(parts) != 5 {
		return Declaration{}, false
	}
	owner, ownerErr := url.PathUnescape(parts[0])
	repo, repoErr := url.PathUnescape(parts[1])
	if ownerErr != nil || repoErr != nil || !githubPartPattern.MatchString(owner) || !githubPartPattern.MatchString(repo) || owner == "." || owner == ".." || repo == "." || repo == ".." {
		return Declaration{}, false
	}
	repository := "https://github.com/" + owner + "/" + repo
	if len(parts) == 2 {
		return Declaration{Kind: GitHubRepository, Repository: repository, Origin: origin}, true
	}
	if parts[2] != "releases" || parts[3] != "tag" {
		return Declaration{}, false
	}
	release, err := url.PathUnescape(parts[4])
	if err != nil || release == "" || strings.Contains(release, "/") {
		return Declaration{}, false
	}
	return Declaration{Kind: GitHubRelease, Repository: repository, Release: release, Origin: origin}, true
}

func deduplicate(declarations []Declaration) ([]Declaration, []Problem) {
	result := make([]Declaration, 0, len(declarations))
	indexes := make(map[string]int, len(declarations))
	var problems []Problem
	for _, declaration := range declarations {
		key := declarationKey(declaration)
		index, exists := indexes[key]
		if !exists {
			indexes[key] = len(result)
			result = append(result, declaration)
			continue
		}
		existing := result[index]
		switch declaration.Kind {
		case Official:
			if existing.Version != "" && declaration.Version != "" && existing.Version != declaration.Version {
				problems = append(problems, problem(ConflictingExactVersion, declaration.Origin, "plugin ID has conflicting exact versions"))
				continue
			}
			if existing.Version == "" && declaration.Version != "" {
				result[index] = declaration
			}
		case GitHubRepository, GitHubRelease:
			if existing.Release != "" && declaration.Release != "" && existing.Release != declaration.Release {
				problems = append(problems, problem(ConflictingExactVersion, declaration.Origin, "GitHub repository has conflicting exact releases"))
				continue
			}
			if existing.Release == "" && declaration.Release != "" {
				result[index] = declaration
			}
		}
	}
	return result, problems
}

func declarationKey(declaration Declaration) string {
	if declaration.Kind == Official {
		return "official:" + declaration.ID
	}
	return "github:" + strings.ToLower(declaration.Repository)
}

func fileLike(raw string) bool {
	lower := strings.ToLower(raw)
	return filepath.IsAbs(raw) || strings.ContainsAny(raw, `/\`) || strings.HasSuffix(lower, ".plugins") || strings.HasSuffix(lower, ".list") || strings.HasSuffix(lower, ".txt")
}

func problem(code ErrorCode, origin Origin, message string) Problem {
	return Problem{Code: code, Origin: origin, Message: message}
}
