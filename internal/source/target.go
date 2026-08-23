package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const DefaultDesktopReleasesURL = "https://raw.githubusercontent.com/obsidianmd/obsidian-releases/HEAD/desktop-releases.json"

// ObsidianVersionProvider resolves the current stable desktop release without
// starting Obsidian or executing installed plugin code.
type ObsidianVersionProvider struct {
	client HTTPDoer
	url    string
}

func NewObsidianVersionProvider(client HTTPDoer, endpoint string) *ObsidianVersionProvider {
	if client == nil {
		client = http.DefaultClient
	}
	if endpoint == "" {
		endpoint = DefaultDesktopReleasesURL
	}
	return &ObsidianVersionProvider{client: client, url: endpoint}
}

func (p *ObsidianVersionProvider) Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return "", fmt.Errorf("read Obsidian compatibility target: %w", err)
	}
	response, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("read Obsidian compatibility target: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("read Obsidian compatibility target: HTTP %d", response.StatusCode)
	}
	var metadata struct {
		LatestVersion string `json:"latestVersion"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&metadata); err != nil || metadata.LatestVersion == "" {
		return "", fmt.Errorf("read Obsidian compatibility target: malformed metadata")
	}
	if _, err := parseSemver(metadata.LatestVersion); err != nil {
		return "", fmt.Errorf("read Obsidian compatibility target: invalid latestVersion: %w", err)
	}
	return metadata.LatestVersion, nil
}
