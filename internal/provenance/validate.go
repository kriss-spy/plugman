// Package provenance defines the trust boundary for persisted plugin source
// metadata shared by mutation and Vault inspection.
package provenance

import (
	"net/url"
	"strings"
	"unicode"
)

// ValidGitHubSource accepts only Plugman's canonical public GitHub repository
// URL and a release tag which cannot be interpreted as a filesystem path.
func ValidGitHubSource(repository, release string) bool {
	parsed, err := url.Parse(repository)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawPath != "" {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(parts) != 2 || !validOwner(parts[0]) || !validRepository(parts[1]) {
		return false
	}
	if repository != "https://github.com/"+parts[0]+"/"+parts[1] {
		return false
	}
	if release == "" || release == "." || release == ".." || strings.ContainsAny(release, `/\`) {
		return false
	}
	return strings.IndexFunc(release, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) == -1
}

func validOwner(owner string) bool {
	if owner == "" || owner[0] == '-' || owner[len(owner)-1] == '-' {
		return false
	}
	for _, r := range owner {
		if !(asciiLetterOrDigit(r) || r == '-') {
			return false
		}
	}
	return true
}

func validRepository(repository string) bool {
	if repository == "" || repository == "." || repository == ".." {
		return false
	}
	for _, r := range repository {
		if !(asciiLetterOrDigit(r) || strings.ContainsRune("-_.", r)) {
			return false
		}
	}
	return true
}

func asciiLetterOrDigit(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}
