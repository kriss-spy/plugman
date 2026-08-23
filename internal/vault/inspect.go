package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kriss-spy/plugman/internal/model"
	"github.com/kriss-spy/plugman/internal/provenance"
)

// ValidateRoot verifies that vaultRoot names a Vault Root containing an
// existing .obsidian directory. Vault owns this filesystem interpretation so
// callers do not duplicate Vault layout knowledge.
func ValidateRoot(vaultRoot string) error {
	configurationPath := filepath.Join(vaultRoot, ".obsidian")
	info, err := os.Stat(configurationPath)
	if err != nil {
		return &model.VaultValidationError{Path: configurationPath, Reason: err.Error()}
	}
	if !info.IsDir() {
		return &model.VaultValidationError{Path: configurationPath, Reason: "not a directory"}
	}
	return nil
}

// Inspect reads all Community Plugin state which can be proven locally.
func Inspect(vaultRoot string) ([]model.PluginObservation, error) {
	configurationPath := filepath.Join(vaultRoot, ".obsidian")
	recoveryPath := filepath.Join(configurationPath, ".plugman", "recovery", "current")
	if _, err := os.Lstat(recoveryPath); err == nil {
		return nil, &model.PendingRecoveryError{Path: recoveryPath}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect pending plugin recovery: %w", err)
	}
	enabled, err := readEnabled(configurationPath)
	if err != nil {
		return nil, err
	}

	pluginsPath := filepath.Join(configurationPath, "plugins")
	entries, err := os.ReadDir(pluginsPath)
	if os.IsNotExist(err) {
		return []model.PluginObservation{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Community Plugin directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	plugins := make([]model.PluginObservation, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			plugins = append(plugins, invalidObservation(entry.Name(), model.Problem{
				Code: "plugin_directory_symlink", Message: "plugin directory must not be a symbolic link",
			}))
			continue
		}
		if !entry.IsDir() {
			continue
		}
		folder := entry.Name()
		manifestPath := filepath.Join(pluginsPath, folder, "manifest.json")
		info, err := os.Lstat(manifestPath)
		if err != nil {
			problem := model.Problem{Code: "manifest_unreadable", Message: "manifest.json cannot be read"}
			if os.IsNotExist(err) {
				problem = model.Problem{Code: "manifest_missing", Message: "manifest.json is missing"}
			}
			plugins = append(plugins, invalidObservation(folder, problem))
			continue
		}
		if !info.Mode().IsRegular() {
			plugins = append(plugins, invalidObservation(folder, model.Problem{
				Code: "manifest_not_regular", Message: "manifest.json must be a regular file",
			}))
			continue
		}
		contents, err := os.ReadFile(manifestPath)
		if err != nil {
			plugins = append(plugins, invalidObservation(folder, model.Problem{
				Code: "manifest_unreadable", Message: "manifest.json cannot be read",
			}))
			continue
		}
		observation := inspectManifest(folder, contents, enabled)
		inspectData(filepath.Join(pluginsPath, folder), &observation)
		readSourceRecord(filepath.Join(pluginsPath, folder), &observation)
		plugins = append(plugins, observation)
	}
	return plugins, nil
}

func inspectData(pluginRoot string, observation *model.PluginObservation) {
	present := false
	observation.HasData = &present
	info, err := os.Lstat(filepath.Join(pluginRoot, "data.json"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		observation.Status = model.PluginInvalid
		observation.Problems = append(observation.Problems, model.Problem{Code: "data_unreadable", Message: "data.json cannot be inspected"})
		return
	}
	if !info.Mode().IsRegular() {
		observation.Status = model.PluginInvalid
		observation.Problems = append(observation.Problems, model.Problem{Code: "data_not_regular", Message: "data.json must be a regular file"})
		return
	}
	present = true
}

func readSourceRecord(pluginRoot string, observation *model.PluginObservation) {
	recordPath := filepath.Join(pluginRoot, ".plugman.json")
	info, err := os.Lstat(recordPath)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		observation.Status = model.PluginInvalid
		observation.Problems = append(observation.Problems, model.Problem{Code: "source_record_unreadable", Message: ".plugman.json cannot be read"})
		return
	}
	if !info.Mode().IsRegular() {
		observation.Status = model.PluginInvalid
		observation.Problems = append(observation.Problems, model.Problem{Code: "source_record_not_regular", Message: ".plugman.json must be a regular file"})
		return
	}
	contents, err := os.ReadFile(recordPath)
	if err != nil {
		observation.Status = model.PluginInvalid
		observation.Problems = append(observation.Problems, model.Problem{Code: "source_record_unreadable", Message: ".plugman.json cannot be read"})
		return
	}
	var record struct {
		Repository string `json:"repository"`
		Release    string `json:"release"`
	}
	if json.Unmarshal(contents, &record) != nil || !provenance.ValidGitHubSource(record.Repository, record.Release) {
		observation.Status = model.PluginInvalid
		observation.Problems = append(observation.Problems, model.Problem{Code: "source_record_invalid", Message: ".plugman.json must contain a public HTTPS GitHub repository and release"})
		return
	}
	observation.Source = model.PluginSource{Kind: model.SourceGitHub, Repository: &record.Repository, Release: &record.Release}
}

func inspectManifest(folder string, contents []byte, enabled map[string]bool) model.PluginObservation {
	var fields struct {
		ID      json.RawMessage `json:"id"`
		Version json.RawMessage `json:"version"`
	}
	if err := json.Unmarshal(contents, &fields); err != nil {
		return invalidObservation(folder, model.Problem{
			Code:    "manifest_invalid_json",
			Message: "manifest.json contains invalid JSON",
		})
	}

	observation := model.PluginObservation{
		Folder:   folder,
		Source:   model.PluginSource{Kind: model.SourceUnknown},
		Status:   model.PluginValid,
		Problems: []model.Problem{},
	}
	observation.ID = nonEmptyJSONString(fields.ID)
	observation.Version = nonEmptyJSONString(fields.Version)
	if observation.ID == nil {
		observation.Problems = append(observation.Problems, model.Problem{
			Code: "manifest_id_invalid", Message: "manifest.json must contain a non-empty string id",
		})
	} else {
		isEnabled := enabled[*observation.ID]
		observation.Enabled = &isEnabled
		if *observation.ID != folder {
			observation.Problems = append(observation.Problems, model.Problem{
				Code: "manifest_id_mismatch", Message: "manifest id does not match the plugin folder",
			})
		}
	}
	if observation.Version == nil {
		observation.Problems = append(observation.Problems, model.Problem{
			Code: "manifest_version_invalid", Message: "manifest.json must contain a non-empty string version",
		})
	}
	if len(observation.Problems) > 0 {
		observation.Status = model.PluginInvalid
	}
	return observation
}

func nonEmptyJSONString(raw json.RawMessage) *string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == "" {
		return nil
	}
	return &value
}

func invalidObservation(folder string, problem model.Problem) model.PluginObservation {
	return model.PluginObservation{
		Folder:   folder,
		Source:   model.PluginSource{Kind: model.SourceUnknown},
		Status:   model.PluginInvalid,
		Problems: []model.Problem{problem},
	}
}

func readEnabled(configurationPath string) (map[string]bool, error) {
	contents, err := os.ReadFile(filepath.Join(configurationPath, "community-plugins.json"))
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read enabled Community Plugins: %w", err)
	}
	var ids []string
	if err := json.Unmarshal(contents, &ids); err != nil {
		return nil, fmt.Errorf("read enabled Community Plugins: %w", err)
	}
	enabled := make(map[string]bool, len(ids))
	for _, id := range ids {
		enabled[id] = true
	}
	return enabled, nil
}
