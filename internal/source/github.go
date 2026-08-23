package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// GitHubConfig contains the GitHub release resolver's external dependencies.
type GitHubConfig struct {
	Client     HTTPDoer
	APIBaseURL string
}

// GitHubProvenance is the information persisted in a GitHub plugin's Source
// Record. It describes where this resolution came from without pinning future
// resolutions to the selected release.
type GitHubProvenance struct {
	Repository string `json:"repository"`
	Release    string `json:"release"`
}

const (
	ErrorInvalidGitHubURL      ErrorCode = "invalid_github_url"
	ErrorGitHubReleaseNotFound ErrorCode = "github_release_not_found"
)

// GitHubResolver resolves explicit public GitHub repository and release URLs.
type GitHubResolver struct {
	client     HTTPDoer
	apiBaseURL string
}

// NewGitHubResolver constructs a resolver for public GitHub releases.
func NewGitHubResolver(config GitHubConfig) *GitHubResolver {
	if config.APIBaseURL == "" {
		config.APIBaseURL = DefaultGitHubAPIBaseURL
	}
	client := config.Client
	if client == nil {
		client = defaultHTTPClient(config.APIBaseURL)
	}
	return &GitHubResolver{client: client, apiBaseURL: strings.TrimRight(config.APIBaseURL, "/")}
}

const defaultHTTPTimeout = 15 * time.Second

func defaultHTTPClient(githubAPIBaseURL string) *http.Client {
	return &http.Client{Timeout: defaultHTTPTimeout, Transport: githubAuthTransport{
		baseURL: strings.TrimRight(githubAPIBaseURL, "/"),
		token:   githubToken(),
		next:    http.DefaultTransport,
	}, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) == 0 || !isGitHubReleaseDownloadURL(via[0].URL) {
			return nil
		}
		return ValidateReleaseAssetRedirectURL(request.URL.String())
	}}
}

type githubAuthTransport struct {
	baseURL string
	token   string
	next    http.RoundTripper
}

func (transport githubAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport.token == "" || !strings.HasPrefix(request.URL.String(), transport.baseURL+"/") {
		return transport.next.RoundTrip(request)
	}
	authenticated := request.Clone(request.Context())
	authenticated.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.next.RoundTrip(authenticated)
}

func githubToken() string {
	if token := strings.TrimSpace(os.Getenv("GH_TOKEN")); token != "" {
		return token
	}
	return strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
}

func isGitHubReleaseDownloadURL(value *url.URL) bool {
	if value == nil || value.Scheme != "https" || !strings.EqualFold(value.Hostname(), "github.com") {
		return false
	}
	segments := strings.Split(strings.Trim(value.EscapedPath(), "/"), "/")
	return len(segments) == 6 && segments[2] == "releases" && segments[3] == "download"
}

type githubInput struct {
	repository string
	tag        string
}

// Resolve returns a validated release and its Source Record provenance. A
// repository URL selects the newest compatible stable release. An exact
// release URL selects that tag and may explicitly select a prerelease.
func (r *GitHubResolver) Resolve(ctx context.Context, input string, target Target) (Release, GitHubProvenance, error) {
	parsedInput, err := parseGitHubInput(input)
	if err != nil {
		return Release{}, GitHubProvenance{}, err
	}
	targetVersion, err := parseSemver(target.ObsidianVersion)
	if err != nil {
		return Release{}, GitHubProvenance{}, malformed("validate target", "invalid Obsidian version", err)
	}

	var repository githubRepository
	endpoint := r.apiBaseURL + "/repos/" + parsedInput.repository
	if err := r.getJSON(ctx, "resolve canonical GitHub repository", endpoint, &repository); err != nil {
		return Release{}, GitHubProvenance{}, err
	}
	if !validRepository(repository.FullName) {
		return Release{}, GitHubProvenance{}, malformed("validate GitHub repository", fmt.Sprintf("GitHub returned invalid canonical repository %q", repository.FullName), nil)
	}

	if parsedInput.tag == "" {
		return r.resolveNewest(ctx, repository.FullName, targetVersion)
	}
	releaseEndpoint := r.apiBaseURL + "/repos/" + repository.FullName + "/releases/tags/" + url.PathEscape(parsedInput.tag)
	var selected githubRelease
	if err := r.getJSON(ctx, "fetch exact GitHub release", releaseEndpoint, &selected); err != nil {
		return Release{}, GitHubProvenance{}, err
	}
	return r.validateRelease(ctx, repository.FullName, selected, parsedInput.tag, targetVersion, true)
}

func parseGitHubInput(value string) (githubInput, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "github.com") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return githubInput{}, &Error{Code: ErrorInvalidGitHubURL, Operation: "parse GitHub input", Message: "expected a public https://github.com/<owner>/<repo> URL"}
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 2 && len(parts) != 5 {
		return githubInput{}, &Error{Code: ErrorInvalidGitHubURL, Operation: "parse GitHub input", Message: "unsupported GitHub URL shape"}
	}
	owner, err := url.PathUnescape(parts[0])
	if err != nil {
		return githubInput{}, &Error{Code: ErrorInvalidGitHubURL, Operation: "parse GitHub input", Message: "invalid repository owner", Err: err}
	}
	repo, err := url.PathUnescape(parts[1])
	if err != nil {
		return githubInput{}, &Error{Code: ErrorInvalidGitHubURL, Operation: "parse GitHub input", Message: "invalid repository name", Err: err}
	}
	repository := owner + "/" + strings.TrimSuffix(repo, ".git")
	if !validRepository(repository) {
		return githubInput{}, &Error{Code: ErrorInvalidGitHubURL, Operation: "parse GitHub input", Message: "invalid GitHub repository"}
	}
	result := githubInput{repository: repository}
	if len(parts) == 5 {
		if parts[2] != "releases" || parts[3] != "tag" || parts[4] == "" {
			return githubInput{}, &Error{Code: ErrorInvalidGitHubURL, Operation: "parse GitHub input", Message: "unsupported GitHub URL shape"}
		}
		result.tag, err = url.PathUnescape(parts[4])
		if err != nil || strings.Contains(result.tag, "/") {
			return githubInput{}, &Error{Code: ErrorInvalidGitHubURL, Operation: "parse GitHub input", Message: "invalid GitHub release tag", Err: err}
		}
	}
	return result, nil
}

func (r *GitHubResolver) resolveNewest(ctx context.Context, repository string, target semver) (Release, GitHubProvenance, error) {
	var releases []githubRelease
	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/releases?per_page=100&page=%d", r.apiBaseURL, repository, page)
		var batch []githubRelease
		if err := r.getJSON(ctx, "list GitHub releases", endpoint, &batch); err != nil {
			return Release{}, GitHubProvenance{}, err
		}
		releases = append(releases, batch...)
		if len(batch) < 100 {
			break
		}
	}

	var selectedRelease Release
	var selectedProvenance GitHubProvenance
	var selectedVersion semver
	found := false
	for _, candidate := range releases {
		if candidate.Draft || candidate.Prerelease {
			continue
		}
		version, err := parseSemver(candidate.TagName)
		if err != nil || version.hasPrerelease() {
			continue
		}
		if found && version.compare(selectedVersion) <= 0 {
			continue
		}
		if _, err := requiredAssets(candidate.Assets); err != nil {
			continue
		}
		release, provenance, err := r.validateRelease(ctx, repository, candidate, candidate.TagName, target, false)
		if err != nil {
			var sourceErr *Error
			if errors.As(err, &sourceErr) && sourceErr.Code == ErrorNoCompatibleRelease {
				continue
			}
			return Release{}, GitHubProvenance{}, err
		}
		selectedRelease, selectedProvenance, selectedVersion = release, provenance, version
		found = true
	}
	if !found {
		return Release{}, GitHubProvenance{}, &Error{Code: ErrorNoCompatibleRelease, Operation: "select GitHub release", Message: fmt.Sprintf("no stable release supports Obsidian %s", target.original)}
	}
	return selectedRelease, selectedProvenance, nil
}

func (r *GitHubResolver) validateRelease(ctx context.Context, repository string, selected githubRelease, exactTag string, target semver, allowPrerelease bool) (Release, GitHubProvenance, error) {
	if selected.TagName != exactTag {
		return Release{}, GitHubProvenance{}, malformed("validate GitHub release", fmt.Sprintf("release tag %q does not exactly match requested tag %q", selected.TagName, exactTag), nil)
	}
	if selected.Draft || (!allowPrerelease && selected.Prerelease) {
		return Release{}, GitHubProvenance{}, malformed("validate GitHub release", "selected release is not eligible", nil)
	}
	if selected.HTMLURL == "" {
		return Release{}, GitHubProvenance{}, malformed("validate GitHub release", "selected release has no web URL", nil)
	}
	releaseInput, err := parseGitHubInput(selected.HTMLURL)
	if err != nil || !strings.EqualFold(releaseInput.repository, repository) || releaseInput.tag != selected.TagName {
		return Release{}, GitHubProvenance{}, malformed("validate GitHub release", "release web URL does not match its canonical repository and tag", err)
	}
	assets, err := requiredAssets(selected.Assets)
	if err != nil {
		return Release{}, GitHubProvenance{}, malformed("validate GitHub release assets", err.Error(), err)
	}
	if err := validateGitHubAssetURLs(assets, repository, selected.TagName); err != nil {
		return Release{}, GitHubProvenance{}, malformed("validate GitHub release assets", err.Error(), err)
	}
	var manifest Manifest
	if err := r.getJSON(ctx, "fetch GitHub release manifest", assets.Manifest.URL, &manifest); err != nil {
		return Release{}, GitHubProvenance{}, err
	}
	if err := validateGitHubManifest(manifest, selected.TagName); err != nil {
		return Release{}, GitHubProvenance{}, malformed("validate GitHub release manifest", err.Error(), err)
	}
	minimum, _ := parseSemver(manifest.MinAppVersion)
	if minimum.compare(target) > 0 {
		return Release{}, GitHubProvenance{}, &Error{Code: ErrorNoCompatibleRelease, Operation: "validate GitHub release compatibility", Message: fmt.Sprintf("release requires Obsidian %s but target is %s", minimum.original, target.original)}
	}
	release := Release{
		PluginID:               manifest.ID,
		Repository:             repository,
		Version:                manifest.Version,
		MinimumObsidianVersion: manifest.MinAppVersion,
		DesktopOnly:            manifest.IsDesktopOnly,
		ReleaseURL:             selected.HTMLURL,
		ReleaseManifest:        manifest,
		Assets:                 assets,
	}
	provenance := GitHubProvenance{Repository: "https://github.com/" + repository, Release: selected.TagName}
	return release, provenance, nil
}

func validateGitHubManifest(manifest Manifest, tag string) error {
	if !validPluginID(manifest.ID) || manifest.Name == "" || manifest.Version == "" || manifest.MinAppVersion == "" {
		return errors.New("manifest is missing id, name, version, or minAppVersion")
	}
	version, err := parseSemver(manifest.Version)
	if err != nil {
		return fmt.Errorf("invalid manifest version: %w", err)
	}
	if manifest.Version != tag || version.original != tag {
		return fmt.Errorf("manifest version %q does not exactly match release tag %q", manifest.Version, tag)
	}
	_, err = parseSemver(manifest.MinAppVersion)
	if err != nil {
		return fmt.Errorf("invalid manifest minAppVersion: %w", err)
	}
	return nil
}

func validPluginID(value string) bool {
	if value == "" || ((value[0] < 'a' || value[0] > 'z') && (value[0] < '0' || value[0] > '9')) {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validateGitHubAssetURLs(assets ReleaseAssets, repository, tag string) error {
	refs := []AssetRef{assets.MainJS, assets.Manifest}
	if assets.Styles != nil {
		refs = append(refs, *assets.Styles)
	}
	for _, ref := range refs {
		if err := ValidateReleaseAssetURL(ref.URL, repository, tag, ref.Name); err != nil {
			return fmt.Errorf("asset %q: %w", ref.Name, err)
		}
	}
	return nil
}

// ValidateReleaseAssetURL verifies the browser_download_url shape documented
// by GitHub for an asset belonging to one exact repository release.
func ValidateReleaseAssetURL(value, repository, tag, assetName string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("URL is not a public GitHub release download URL")
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) != 6 || segments[2] != "releases" || segments[3] != "download" {
		return errors.New("URL does not have GitHub's release asset path shape")
	}
	decoded := make([]string, len(segments))
	for index, segment := range segments {
		decoded[index], err = url.PathUnescape(segment)
		if err != nil || strings.Contains(decoded[index], "/") {
			return errors.New("URL contains an invalid escaped path segment")
		}
	}
	if !strings.EqualFold(decoded[0]+"/"+decoded[1], repository) || decoded[4] != tag || decoded[5] != assetName {
		return errors.New("URL does not match the resolved repository, release tag, and asset name")
	}
	return nil
}

// ValidateReleaseAssetRedirectURL restricts a followed asset redirect to the
// GitHub-controlled hosts documented for release delivery.
func ValidateReleaseAssetRedirectURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Port() != "" || parsed.User != nil || parsed.Fragment != "" || parsed.Path == "" || parsed.Path == "/" {
		return errors.New("redirect is not a public HTTPS GitHub asset URL")
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com", "objects-origin.githubusercontent.com", "github-releases.githubusercontent.com":
		return nil
	default:
		return fmt.Errorf("redirect host %q is not a GitHub release asset host", host)
	}
}

func (r *GitHubResolver) getJSON(ctx context.Context, operation, endpoint string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return malformed(operation, "invalid metadata URL", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json, application/json")
	response, err := r.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Code: ErrorTransportFailure, Operation: operation, URL: endpoint, Message: "could not reach release service", Err: err}
	}
	defer response.Body.Close()
	if strings.Contains(operation, "release manifest") && response.Request != nil && response.Request.URL != nil && response.Request.URL.String() != endpoint {
		if err := ValidateReleaseAssetRedirectURL(response.Request.URL.String()); err != nil {
			return malformed(operation, "release asset redirected outside GitHub", err)
		}
	}
	if response.StatusCode == http.StatusTooManyRequests || (response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0") {
		return &Error{Code: ErrorRateLimited, Operation: operation, URL: endpoint, StatusCode: response.StatusCode, Message: "release service rate limit exceeded"}
	}
	if response.StatusCode == http.StatusNotFound && strings.Contains(operation, "release") {
		return &Error{Code: ErrorGitHubReleaseNotFound, Operation: operation, URL: endpoint, StatusCode: response.StatusCode, Message: "GitHub release was not found"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &Error{Code: ErrorUpstreamFailure, Operation: operation, URL: endpoint, StatusCode: response.StatusCode, Message: fmt.Sprintf("release service returned HTTP %d", response.StatusCode)}
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := decoder.Decode(destination); err != nil {
		return malformed(operation, "release service returned malformed JSON", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return malformed(operation, "release service returned trailing JSON data", err)
	}
	return nil
}
