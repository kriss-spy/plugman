// Package stage downloads and validates release assets without executing them.
package stage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/kriss-spy/plugman/internal/source"
)

const (
	defaultMaxManifestBytes = 1 << 20
	defaultMaxMainJSBytes   = 64 << 20
	defaultMaxStylesBytes   = 16 << 20
)

// Config supplies the stager's network dependency and resource limits.
// AllowedTestOrigins permits explicit non-HTTPS origins in deterministic tests.
type Config struct {
	Client             source.HTTPDoer
	AllowedTestOrigins []string
	MaxManifestBytes   int64
	MaxMainJSBytes     int64
	MaxStylesBytes     int64
}

// Request describes one release and its new, empty staging destination.
type Request struct {
	VaultRoot   string
	Destination string
	Release     source.Release
}

// Prepared is a fully downloaded and validated release directory.
type Prepared struct {
	Release   source.Release
	Dir       string
	BatchRoot string
	cleanup   string
}

// Cleanup removes this prepared release or its complete preflight batch.
func (p Prepared) Cleanup() error {
	if p.cleanup == "" {
		return nil
	}
	return os.RemoveAll(p.cleanup)
}

// Stager downloads release assets into a Vault-local staging area.
type Stager struct {
	client           source.HTTPDoer
	allowedOrigins   map[string]struct{}
	maxManifestBytes int64
	maxMainJSBytes   int64
	maxStylesBytes   int64
}

// New creates a release asset stager.
func New(config Config) *Stager {
	client := config.Client
	if client == nil {
		client = &http.Client{CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) == 0 || !isGitHubReleaseDownloadStart(via[0].URL) {
				return nil
			}
			return source.ValidateReleaseAssetRedirectURL(request.URL.String())
		}}
	}
	return &Stager{
		client:           client,
		allowedOrigins:   normalizedOrigins(config.AllowedTestOrigins),
		maxManifestBytes: positiveOr(config.MaxManifestBytes, defaultMaxManifestBytes),
		maxMainJSBytes:   positiveOr(config.MaxMainJSBytes, defaultMaxMainJSBytes),
		maxStylesBytes:   positiveOr(config.MaxStylesBytes, defaultMaxStylesBytes),
	}
}

// Stage downloads and validates one Release. Destination must not exist and
// must be below <Vault Root>/.obsidian/.plugman/staging.
func (s *Stager) Stage(ctx context.Context, request Request) (prepared Prepared, err error) {
	if err := validateRelease(request.Release); err != nil {
		return prepared, err
	}
	destination, err := prepareDestination(request.VaultRoot, request.Destination)
	if err != nil {
		return prepared, err
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return prepared, fmt.Errorf("create staging destination: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(destination)
		}
	}()

	assets := []struct {
		ref   source.AssetRef
		name  string
		limit int64
	}{
		{request.Release.Assets.Manifest, "manifest.json", s.maxManifestBytes},
		{request.Release.Assets.MainJS, "main.js", s.maxMainJSBytes},
	}
	if request.Release.Assets.Styles != nil {
		assets = append(assets, struct {
			ref   source.AssetRef
			name  string
			limit int64
		}{*request.Release.Assets.Styles, "styles.css", s.maxStylesBytes})
	}
	for _, asset := range assets {
		if err := s.download(ctx, request.Release, asset.ref, asset.name, filepath.Join(destination, asset.name), asset.limit); err != nil {
			return prepared, err
		}
	}
	if err := validateDownloadedManifest(filepath.Join(destination, "manifest.json"), request.Release); err != nil {
		return prepared, err
	}
	if err := verifyRegularAssets(destination, len(assets)); err != nil {
		return prepared, err
	}

	complete = true
	return Prepared{Release: request.Release, Dir: destination, cleanup: destination}, nil
}

// Preflight stages the complete release batch before any caller-visible
// mutation. Returned entries retain declaration order. Any failure removes the
// entire batch.
func (s *Stager) Preflight(ctx context.Context, vaultRoot string, releases []source.Release) ([]Prepared, error) {
	if len(releases) == 0 {
		return []Prepared{}, nil
	}
	stagingRoot, err := ensureStagingRoot(vaultRoot)
	if err != nil {
		return nil, err
	}
	batchRoot, err := os.MkdirTemp(stagingRoot, "batch-")
	if err != nil {
		return nil, fmt.Errorf("create batch staging directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(batchRoot)
		}
	}()

	prepared := make([]Prepared, 0, len(releases))
	for index, release := range releases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		destination := filepath.Join(batchRoot, fmt.Sprintf("%03d-%s", index, release.PluginID))
		item, err := s.Stage(ctx, Request{VaultRoot: vaultRoot, Destination: destination, Release: release})
		if err != nil {
			return nil, fmt.Errorf("stage plugin %q: %w", release.PluginID, err)
		}
		item.BatchRoot = batchRoot
		item.cleanup = batchRoot
		prepared = append(prepared, item)
	}
	keep = true
	return prepared, nil
}

// PreflightDryRun performs the same downloads and asset validation in a
// disposable system temporary Vault. It never creates .plugman state in the
// user's Vault, and Cleanup removes the complete temporary root.
func (s *Stager) PreflightDryRun(ctx context.Context, _ string, releases []source.Release) ([]Prepared, error) {
	if len(releases) == 0 {
		return []Prepared{}, nil
	}
	temporaryVault, err := os.MkdirTemp("", "plugman-dry-run-")
	if err != nil {
		return nil, fmt.Errorf("create disposable dry-run staging: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(temporaryVault)
		}
	}()
	if err := os.Mkdir(filepath.Join(temporaryVault, ".obsidian"), 0o700); err != nil {
		return nil, fmt.Errorf("create disposable dry-run Vault: %w", err)
	}
	prepared, err := s.Preflight(ctx, temporaryVault, releases)
	if err != nil {
		return nil, err
	}
	for index := range prepared {
		prepared[index].cleanup = temporaryVault
	}
	keep = true
	return prepared, nil
}

func (s *Stager) download(ctx context.Context, release source.Release, ref source.AssetRef, expectedName, path string, limit int64) error {
	if ref.Name != expectedName {
		return fmt.Errorf("validate %s asset: expected asset name %q, got %q", expectedName, expectedName, ref.Name)
	}
	assetURL, err := s.validateInitialURL(release, ref)
	if err != nil {
		return fmt.Errorf("validate %s asset URL: %w", expectedName, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL.String(), nil)
	if err != nil {
		return fmt.Errorf("download %s: %w", expectedName, err)
	}
	request.Header.Set("Accept", "application/octet-stream")
	response, err := s.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("download %s: %w", expectedName, err)
	}
	if response == nil || response.Body == nil {
		return fmt.Errorf("download %s: HTTP client returned an empty response", expectedName)
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL == nil {
		return fmt.Errorf("validate final %s asset URL: HTTP client did not report its final request URL", expectedName)
	}
	if response.Request.URL.String() != assetURL.String() {
		if err := s.validateFinalURL(response.Request.URL); err != nil {
			return fmt.Errorf("validate final %s asset URL: %w", expectedName, err)
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download %s: release service returned HTTP %d", expectedName, response.StatusCode)
	}
	if response.ContentLength > limit {
		return fmt.Errorf("download %s: asset exceeds %d-byte size limit", expectedName, limit)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create staged %s: %w", expectedName, err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, limit+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("download %s: %w", expectedName, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close staged %s: %w", expectedName, closeErr)
	}
	if written > limit {
		return fmt.Errorf("download %s: asset exceeds %d-byte size limit", expectedName, limit)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("validate staged %s: asset must be a regular file", expectedName)
	}
	return nil
}

func isGitHubReleaseDownloadStart(value *url.URL) bool {
	if value == nil || value.Scheme != "https" || !strings.EqualFold(value.Hostname(), "github.com") {
		return false
	}
	segments := strings.Split(strings.Trim(value.EscapedPath(), "/"), "/")
	return len(segments) == 6 && segments[2] == "releases" && segments[3] == "download"
}

func (s *Stager) validateInitialURL(release source.Release, ref source.AssetRef) (*url.URL, error) {
	parsed, err := s.parseURL(ref.URL)
	if err != nil {
		return nil, err
	}
	if s.isAllowedTestOrigin(parsed) {
		return parsed, nil
	}
	if err := source.ValidateReleaseAssetURL(ref.URL, release.Repository, release.Version, ref.Name); err != nil {
		return nil, err
	}
	return parsed, nil
}

func (s *Stager) validateFinalURL(parsed *url.URL) error {
	if s.isAllowedTestOrigin(parsed) {
		return nil
	}
	return source.ValidateReleaseAssetRedirectURL(parsed.String())
}

func (s *Stager) parseURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("asset URL is malformed")
	}
	if parsed.Scheme == "https" || s.isAllowedTestOrigin(parsed) {
		return parsed, nil
	}
	return nil, errors.New("asset URL must use HTTPS")
}

func (s *Stager) isAllowedTestOrigin(parsed *url.URL) bool {
	_, ok := s.allowedOrigins[urlOrigin(parsed)]
	return ok
}

func validateRelease(release source.Release) error {
	if !validPluginID(release.PluginID) {
		return fmt.Errorf("validate release: invalid plugin ID %q", release.PluginID)
	}
	if strings.TrimSpace(release.Version) == "" {
		return errors.New("validate release: version is required")
	}
	if strings.TrimSpace(release.MinimumObsidianVersion) == "" {
		return errors.New("validate release: minimum Obsidian version is required")
	}
	if release.Assets.Manifest.Name == "" || release.Assets.Manifest.URL == "" || release.Assets.MainJS.Name == "" || release.Assets.MainJS.URL == "" {
		return errors.New("validate release: manifest.json and main.js asset references are required")
	}
	return nil
}

func validateDownloadedManifest(path string, release source.Release) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("validate downloaded manifest: %w", err)
	}
	defer file.Close()
	var manifest source.Manifest
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("validate downloaded manifest: malformed JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return fmt.Errorf("validate downloaded manifest: trailing data: %w", err)
	}
	if manifest.ID != release.PluginID {
		return fmt.Errorf("validate downloaded manifest: manifest id %q does not match release plugin ID %q", manifest.ID, release.PluginID)
	}
	if manifest.Version != release.Version {
		return fmt.Errorf("validate downloaded manifest: manifest version %q does not match release version %q", manifest.Version, release.Version)
	}
	if manifest.MinAppVersion != release.MinimumObsidianVersion {
		return fmt.Errorf("validate downloaded manifest: manifest minAppVersion %q does not match release minimum %q", manifest.MinAppVersion, release.MinimumObsidianVersion)
	}
	return nil
}

func verifyRegularAssets(destination string, expectedCount int) error {
	directory, err := os.Lstat(destination)
	if err != nil || !directory.IsDir() || directory.Mode()&os.ModeSymlink != 0 {
		return errors.New("validate staging destination: destination must be a regular directory")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return fmt.Errorf("validate staged assets: %w", err)
	}
	if len(entries) != expectedCount {
		return fmt.Errorf("validate staged assets: expected %d regular files, found %d entries", expectedCount, len(entries))
	}
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(destination, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("validate staged asset %q: asset must be a regular file", entry.Name())
		}
	}
	return nil
}

func prepareDestination(vaultRoot, destination string) (string, error) {
	stagingRoot, err := stagingPath(vaultRoot)
	if err != nil {
		return "", err
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return "", fmt.Errorf("resolve staging destination: %w", err)
	}
	relative, err := filepath.Rel(stagingRoot, absDestination)
	if err != nil || relative == "." || relative == "" || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("staging destination must be a child of the Vault staging area")
	}
	stagingRoot, err = ensureStagingRoot(vaultRoot)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(absDestination)
	if err := ensureDirectoryTree(stagingRoot, parent); err != nil {
		return "", err
	}
	if _, err := os.Lstat(absDestination); err == nil {
		return "", errors.New("staging destination must not already exist")
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect staging destination: %w", err)
	}
	return absDestination, nil
}

func ensureStagingRoot(vaultRoot string) (string, error) {
	staging, err := stagingPath(vaultRoot)
	if err != nil {
		return "", err
	}
	plugman := filepath.Dir(staging)
	if err := requireRegularDirectory(plugman, true); err != nil {
		return "", err
	}
	if err := requireRegularDirectory(staging, true); err != nil {
		return "", err
	}
	return staging, nil
}

func stagingPath(vaultRoot string) (string, error) {
	if vaultRoot == "" {
		return "", errors.New("Vault Root is required")
	}
	root, err := filepath.Abs(vaultRoot)
	if err != nil {
		return "", fmt.Errorf("resolve Vault Root: %w", err)
	}
	obsidian := filepath.Join(root, ".obsidian")
	if err := requireRegularDirectory(obsidian, false); err != nil {
		return "", fmt.Errorf("Vault Root must contain a regular .obsidian directory: %w", err)
	}
	return filepath.Join(obsidian, ".plugman", "staging"), nil
}

func ensureDirectoryTree(root, destination string) error {
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("staging destination must be a child of the Vault staging area")
	}
	current := root
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		if err := requireRegularDirectory(current, true); err != nil {
			return err
		}
	}
	return nil
}

func requireRegularDirectory(path string, create bool) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) && create {
		if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
			return fmt.Errorf("create managed directory %s: %w", path, err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect managed directory %s: %w", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed path %s must be a regular directory", path)
	}
	return nil
}

func validPluginID(value string) bool {
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || (index > 0 && (char == '-' || char == '_' || char == '.')) {
			continue
		}
		return false
	}
	return true
}

func normalizedOrigins(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		parsed, err := url.Parse(value)
		if err == nil && parsed.Scheme != "" && parsed.Host != "" && parsed.User == nil {
			result[urlOrigin(parsed)] = struct{}{}
		}
	}
	return result
}

func urlOrigin(value *url.URL) string {
	return strings.ToLower(value.Scheme) + "://" + strings.ToLower(value.Host)
}

func positiveOr(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}
