// Package source resolves plugin declarations into validated release metadata.
package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// HTTPDoer is the portion of http.Client needed by release resolvers.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// OfficialConfig contains the official-directory resolver's external endpoints.
// Endpoint injection keeps tests deterministic and avoids package-global clients.
type OfficialConfig struct {
	Client           HTTPDoer
	RegistryURL      string
	RawBaseURL       string
	GitHubAPIBaseURL string
}

const (
	DefaultRegistryURL      = "https://raw.githubusercontent.com/obsidianmd/obsidian-releases/HEAD/community-plugins.json"
	DefaultRawBaseURL       = "https://raw.githubusercontent.com"
	DefaultGitHubAPIBaseURL = "https://api.github.com"
)

// Target describes the Obsidian installation a release must support.
type Target struct {
	ObsidianVersion string
}

// Manifest contains the Obsidian manifest fields used during resolution.
type Manifest struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Author        string `json:"author"`
	Description   string `json:"description"`
	Version       string `json:"version"`
	MinAppVersion string `json:"minAppVersion"`
	IsDesktopOnly bool   `json:"isDesktopOnly"`
}

// AssetRef identifies a release asset without downloading its contents.
type AssetRef struct {
	Name string
	URL  string
}

// ReleaseAssets are the files needed to install an Obsidian plugin.
type ReleaseAssets struct {
	MainJS   AssetRef
	Manifest AssetRef
	Styles   *AssetRef
}

// Release is a validated official plugin release. MainJS and Styles contain
// references only; Resolve downloads only the small release manifest.
type Release struct {
	PluginID               string
	Repository             string
	Version                string
	MinimumObsidianVersion string
	DesktopOnly            bool
	ReleaseURL             string
	RootManifest           Manifest
	ReleaseManifest        Manifest
	Assets                 ReleaseAssets
}

// ErrorCode is a stable, caller-facing release resolution failure category.
type ErrorCode string

const (
	ErrorOfficialPluginNotFound ErrorCode = "official_plugin_not_found"
	ErrorNoCompatibleRelease    ErrorCode = "no_compatible_release"
	ErrorMalformedMetadata      ErrorCode = "malformed_metadata"
	ErrorRateLimited            ErrorCode = "rate_limited"
	ErrorTransportFailure       ErrorCode = "transport_failure"
	ErrorUpstreamFailure        ErrorCode = "upstream_failure"
)

// Error describes a resolution failure without exposing transport-specific
// classification logic to callers.
type Error struct {
	Code       ErrorCode
	Operation  string
	URL        string
	StatusCode int
	Message    string
	Err        error
}

func (e *Error) Error() string {
	message := e.Message
	if message == "" && e.Err != nil {
		message = e.Err.Error()
	}
	if message == "" {
		message = string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Operation, message)
}

func (e *Error) Unwrap() error { return e.Err }

// OfficialResolver resolves IDs found in Obsidian's community plugin registry.
type OfficialResolver struct {
	client           HTTPDoer
	registryURL      string
	rawBaseURL       string
	githubAPIBaseURL string
	registryMu       sync.Mutex
	registry         []registryEntry
	registryLoaded   bool
}

// NewOfficialResolver constructs an official-directory resolver.
func NewOfficialResolver(config OfficialConfig) *OfficialResolver {
	if config.RegistryURL == "" {
		config.RegistryURL = DefaultRegistryURL
	}
	if config.RawBaseURL == "" {
		config.RawBaseURL = DefaultRawBaseURL
	}
	if config.GitHubAPIBaseURL == "" {
		config.GitHubAPIBaseURL = DefaultGitHubAPIBaseURL
	}
	client := config.Client
	if client == nil {
		client = defaultHTTPClient(config.GitHubAPIBaseURL)
	}
	return &OfficialResolver{
		client:           client,
		registryURL:      strings.TrimRight(config.RegistryURL, "/"),
		rawBaseURL:       strings.TrimRight(config.RawBaseURL, "/"),
		githubAPIBaseURL: strings.TrimRight(config.GitHubAPIBaseURL, "/"),
	}
}

type registryEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Author      string `json:"author"`
	Description string `json:"description"`
	Repo        string `json:"repo"`
}

type githubRepository struct {
	FullName string `json:"full_name"`
}

// OfficialPlugin is an identity recognized by Obsidian's community directory.
// Repository is GitHub's current canonical owner/name, including transfers.
type OfficialPlugin struct {
	ID          string
	Name        string
	Author      string
	Description string
	Repository  string
}

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	HTMLURL    string        `json:"html_url"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Recognize checks exact community-directory membership without resolving a
// repository or release. Successful registry reads are reused by this resolver.
func (r *OfficialResolver) Recognize(ctx context.Context, officialID string) (bool, error) {
	_, found, err := r.findRegistryEntry(ctx, officialID)
	return found, err
}

// Lookup resolves an exact official plugin ID and its current canonical GitHub
// repository without fetching manifests, releases, or compatibility metadata.
func (r *OfficialResolver) Lookup(ctx context.Context, officialID string) (OfficialPlugin, error) {
	entry, found, err := r.findRegistryEntry(ctx, officialID)
	if err != nil {
		return OfficialPlugin{}, err
	}
	if !found {
		return OfficialPlugin{}, &Error{Code: ErrorOfficialPluginNotFound, Operation: "resolve official plugin", Message: fmt.Sprintf("plugin %q is not in Obsidian's community directory", officialID)}
	}

	var repository githubRepository
	repositoryURL := r.githubAPIBaseURL + "/repos/" + entry.Repo
	if err := r.getJSON(ctx, "resolve canonical repository", repositoryURL, &repository); err != nil {
		return OfficialPlugin{}, err
	}
	if !validRepository(repository.FullName) {
		return OfficialPlugin{}, malformed("validate GitHub repository", fmt.Sprintf("GitHub returned invalid canonical repository %q", repository.FullName), nil)
	}

	return OfficialPlugin{
		ID:          entry.ID,
		Name:        entry.Name,
		Author:      entry.Author,
		Description: entry.Description,
		Repository:  repository.FullName,
	}, nil
}

func (r *OfficialResolver) findRegistryEntry(ctx context.Context, officialID string) (registryEntry, bool, error) {
	if officialID == "" {
		return registryEntry{}, false, malformed("validate official plugin ID", "official plugin ID is empty", nil)
	}
	registry, err := r.loadRegistry(ctx)
	if err != nil {
		return registryEntry{}, false, err
	}
	var match registryEntry
	found := false
	for _, entry := range registry {
		if entry.ID != officialID {
			continue
		}
		if found {
			return registryEntry{}, false, malformed("validate official directory", fmt.Sprintf("plugin ID %q appears more than once", officialID), nil)
		}
		match = entry
		found = true
	}
	if !found {
		return registryEntry{}, false, nil
	}
	if !validRepository(match.Repo) {
		return registryEntry{}, false, malformed("validate official directory", fmt.Sprintf("plugin %q has invalid repository %q", officialID, match.Repo), nil)
	}
	return match, true, nil
}

func (r *OfficialResolver) loadRegistry(ctx context.Context) ([]registryEntry, error) {
	r.registryMu.Lock()
	defer r.registryMu.Unlock()
	if r.registryLoaded {
		return r.registry, nil
	}
	var registry []registryEntry
	if err := r.getJSON(ctx, "fetch official directory", r.registryURL, &registry); err != nil {
		return nil, err
	}
	r.registry = registry
	r.registryLoaded = true
	return r.registry, nil
}

// Resolve selects the newest compatible, non-prerelease GitHub release for an
// exact official plugin ID.
func (r *OfficialResolver) Resolve(ctx context.Context, officialID string, target Target) (Release, error) {
	targetVersion, err := parseSemver(target.ObsidianVersion)
	if err != nil {
		return Release{}, malformed("validate target", "invalid Obsidian version", err)
	}

	plugin, err := r.Lookup(ctx, officialID)
	if err != nil {
		return Release{}, err
	}

	rootManifestURL := r.rawURL(plugin.Repository, "manifest.json")
	var rootManifest Manifest
	if err := r.getJSON(ctx, "fetch root manifest", rootManifestURL, &rootManifest); err != nil {
		return Release{}, err
	}
	if err := validateRootManifest(rootManifest, officialID); err != nil {
		return Release{}, malformed("validate root manifest", err.Error(), err)
	}

	selectedVersion, minimumVersion, err := r.selectCompatibleVersion(ctx, plugin.Repository, rootManifest, targetVersion)
	if err != nil {
		return Release{}, err
	}

	releaseURL := r.githubAPIBaseURL + "/repos/" + plugin.Repository + "/releases/tags/" + url.PathEscape(selectedVersion.original)
	var selected githubRelease
	if err := r.getJSON(ctx, "fetch GitHub release", releaseURL, &selected); err != nil {
		return Release{}, err
	}
	if selected.TagName != selectedVersion.original {
		return Release{}, malformed("validate GitHub release", fmt.Sprintf("release tag %q does not exactly match manifest version %q", selected.TagName, selectedVersion.original), nil)
	}
	if selected.Draft || selected.Prerelease {
		return Release{}, malformed("validate GitHub release", fmt.Sprintf("release %q is draft or prerelease", selectedVersion.original), nil)
	}
	if selected.HTMLURL == "" {
		return Release{}, malformed("validate GitHub release", "selected release has no web URL", nil)
	}
	assets, err := requiredAssets(selected.Assets)
	if err != nil {
		return Release{}, malformed("validate release assets", err.Error(), err)
	}
	if err := validateGitHubAssetURLs(assets, plugin.Repository, selectedVersion.original); err != nil {
		return Release{}, malformed("validate release assets", err.Error(), err)
	}

	var releaseManifest Manifest
	if err := r.getJSON(ctx, "fetch release manifest", assets.Manifest.URL, &releaseManifest); err != nil {
		return Release{}, err
	}
	if err := validateReleaseManifest(releaseManifest, officialID, selectedVersion.original, minimumVersion.original); err != nil {
		return Release{}, malformed("validate release manifest", err.Error(), err)
	}

	return Release{
		PluginID:               officialID,
		Repository:             plugin.Repository,
		Version:                selectedVersion.original,
		MinimumObsidianVersion: minimumVersion.original,
		DesktopOnly:            releaseManifest.IsDesktopOnly,
		ReleaseURL:             selected.HTMLURL,
		RootManifest:           rootManifest,
		ReleaseManifest:        releaseManifest,
		Assets:                 assets,
	}, nil
}

// Inspect resolves release metadata needed by read-only commands without
// performing GitHub API or release-asset validation. Mutating operations must
// continue to use Resolve or ResolveExact.
func (r *OfficialResolver) Inspect(ctx context.Context, officialID string, target Target) (Release, error) {
	targetVersion, err := parseSemver(target.ObsidianVersion)
	if err != nil {
		return Release{}, malformed("validate target", "invalid Obsidian version", err)
	}
	entry, found, err := r.findRegistryEntry(ctx, officialID)
	if err != nil {
		return Release{}, err
	}
	if !found {
		return Release{}, &Error{Code: ErrorOfficialPluginNotFound, Operation: "resolve official plugin", Message: fmt.Sprintf("plugin %q is not in Obsidian's community directory", officialID)}
	}

	rootManifestURL := r.rawURL(entry.Repo, "manifest.json")
	var rootManifest Manifest
	if err := r.getJSON(ctx, "fetch root manifest", rootManifestURL, &rootManifest); err != nil {
		return Release{}, err
	}
	if err := validateRootManifest(rootManifest, officialID); err != nil {
		return Release{}, malformed("validate root manifest", err.Error(), err)
	}
	selectedVersion, minimumVersion, err := r.selectCompatibleVersion(ctx, entry.Repo, rootManifest, targetVersion)
	if err != nil {
		return Release{}, err
	}
	selectedManifest := rootManifest
	if selectedVersion.original != rootManifest.Version {
		manifestURL := r.rawRefURL(entry.Repo, selectedVersion.original, "manifest.json")
		if err := r.getJSON(ctx, "fetch compatible manifest", manifestURL, &selectedManifest); err != nil {
			return Release{}, err
		}
	}
	if err := validateReleaseManifest(selectedManifest, officialID, selectedVersion.original, minimumVersion.original); err != nil {
		return Release{}, malformed("validate compatible manifest", err.Error(), err)
	}
	return Release{
		PluginID:               officialID,
		Repository:             entry.Repo,
		Version:                selectedVersion.original,
		MinimumObsidianVersion: minimumVersion.original,
		DesktopOnly:            selectedManifest.IsDesktopOnly,
		ReleaseURL:             "https://github.com/" + entry.Repo + "/releases/tag/" + url.PathEscape(selectedVersion.original),
		RootManifest:           rootManifest,
		ReleaseManifest:        selectedManifest,
	}, nil
}

// ResolveExact resolves only one explicitly requested release for an identity
// previously returned by Lookup. It does not inspect the repository root,
// versions.json, or any other release.
func (r *OfficialResolver) ResolveExact(ctx context.Context, plugin OfficialPlugin, version string, target Target) (Release, error) {
	if !validPluginID(plugin.ID) || !validRepository(plugin.Repository) {
		return Release{}, malformed("validate exact official plugin", "official identity or canonical repository is invalid", nil)
	}
	if _, err := parseSemver(version); err != nil {
		return Release{}, malformed("validate exact official version", "requested version is not semantic", err)
	}
	targetVersion, err := parseSemver(target.ObsidianVersion)
	if err != nil {
		return Release{}, malformed("validate target", "invalid Obsidian version", err)
	}

	endpoint := r.githubAPIBaseURL + "/repos/" + plugin.Repository + "/releases/tags/" + url.PathEscape(version)
	var selected githubRelease
	if err := r.getJSON(ctx, "fetch exact official release", endpoint, &selected); err != nil {
		return Release{}, err
	}
	github := &GitHubResolver{client: r.client, apiBaseURL: r.githubAPIBaseURL}
	release, _, err := github.validateRelease(ctx, plugin.Repository, selected, version, targetVersion, true)
	if err != nil {
		return Release{}, err
	}
	if release.PluginID != plugin.ID {
		return Release{}, malformed("validate exact official release", fmt.Sprintf("release manifest ID %q does not match official ID %q", release.PluginID, plugin.ID), nil)
	}
	return release, nil
}

func (r *OfficialResolver) rawURL(repository, filename string) string {
	return r.rawRefURL(repository, "HEAD", filename)
}

func (r *OfficialResolver) rawRefURL(repository, ref, filename string) string {
	return r.rawBaseURL + "/" + repository + "/" + url.PathEscape(ref) + "/" + filename
}

func (r *OfficialResolver) getJSON(ctx context.Context, operation, endpoint string, destination any) error {
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

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func malformed(operation, message string, err error) *Error {
	return &Error{Code: ErrorMalformedMetadata, Operation: operation, Message: message, Err: err}
}

func validRepository(repository string) bool {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		if part == "." || part == ".." || strings.ContainsAny(part, "\\?#%") {
			return false
		}
	}
	return true
}

func validateRootManifest(manifest Manifest, officialID string) error {
	if manifest.ID != officialID {
		return fmt.Errorf("manifest ID %q does not match official ID %q", manifest.ID, officialID)
	}
	if manifest.Name == "" || manifest.Version == "" {
		return fmt.Errorf("manifest is missing %s", missingManifestFields(manifest))
	}
	if _, err := parseSemver(manifest.Version); err != nil {
		return fmt.Errorf("invalid manifest version: %w", err)
	}
	// minAppVersion is optional in Obsidian manifests; an absent value means
	// the plugin declares no minimum and is compatible with every release.
	if manifest.MinAppVersion != "" {
		if _, err := parseSemver(manifest.MinAppVersion); err != nil {
			return fmt.Errorf("invalid manifest minAppVersion: %w", err)
		}
	}
	return nil
}

func validateReleaseManifest(manifest Manifest, officialID, version, minimum string) error {
	if err := validateRootManifest(manifest, officialID); err != nil {
		return err
	}
	manifestVersion, _ := parseSemver(manifest.Version)
	selectedVersion, _ := parseSemver(version)
	if manifestVersion.compare(selectedVersion) != 0 || manifest.Version != version {
		return fmt.Errorf("manifest version %q does not match release version %q", manifest.Version, version)
	}
	manifestMinimum, _ := parseSemver(manifest.MinAppVersion)
	selectedMinimum, _ := parseSemver(minimum)
	if manifestMinimum.compare(selectedMinimum) != 0 {
		return fmt.Errorf("manifest minAppVersion %q does not match versions.json value %q", manifest.MinAppVersion, minimum)
	}
	return nil
}

func (r *OfficialResolver) selectCompatibleVersion(ctx context.Context, repository string, root Manifest, target semver) (semver, semver, error) {
	rootVersion, _ := parseSemver(root.Version)
	rootMinimum, _ := parseSemver(root.MinAppVersion)
	if !rootVersion.hasPrerelease() && rootMinimum.compare(target) <= 0 {
		return rootVersion, rootMinimum, nil
	}

	versionsURL := r.rawURL(repository, "versions.json")
	versions := make(map[string]string)
	if err := r.getJSON(ctx, "fetch compatibility metadata", versionsURL, &versions); err != nil {
		return semver{}, semver{}, err
	}

	var selectedVersion semver
	var selectedMinimum semver
	found := false
	for versionText, minimumText := range versions {
		version, err := parseSemver(versionText)
		if err != nil {
			return semver{}, semver{}, malformed("validate compatibility metadata", fmt.Sprintf("versions.json contains invalid plugin version %q", versionText), err)
		}
		minimum, err := parseSemver(minimumText)
		if err != nil {
			return semver{}, semver{}, malformed("validate compatibility metadata", fmt.Sprintf("release %q has invalid minimum Obsidian version %q", versionText, minimumText), err)
		}
		if version.hasPrerelease() || minimum.compare(target) > 0 {
			continue
		}
		if !found || version.compare(selectedVersion) > 0 {
			selectedVersion = version
			selectedMinimum = minimum
			found = true
		}
	}
	if !found {
		return semver{}, semver{}, &Error{Code: ErrorNoCompatibleRelease, Operation: "select official release", Message: fmt.Sprintf("no stable release supports Obsidian %s", target.original)}
	}
	return selectedVersion, selectedMinimum, nil
}

func requiredAssets(githubAssets []githubAsset) (ReleaseAssets, error) {
	var assets ReleaseAssets
	for _, asset := range githubAssets {
		if asset.URL == "" {
			continue
		}
		ref := AssetRef{Name: asset.Name, URL: asset.URL}
		switch asset.Name {
		case "main.js":
			assets.MainJS = ref
		case "manifest.json":
			assets.Manifest = ref
		case "styles.css":
			copy := ref
			assets.Styles = &copy
		}
	}
	if assets.MainJS.URL == "" || assets.Manifest.URL == "" {
		return ReleaseAssets{}, errors.New("release must include main.js and manifest.json assets")
	}
	return assets, nil
}
