package manager

import (
	"context"
	"os"
	"path/filepath"

	"github.com/kriss-spy/plugman/internal/model"
	"github.com/kriss-spy/plugman/internal/vault"
)

// Manager executes Plugman operations for one candidate Vault Root.
type Manager interface {
	Run(context.Context, model.Operation) (model.Report, error)
}

type manager struct {
	vaultRoot string
}

// New creates a Manager rooted at the exact directory Plugman should manage.
func New(vaultRoot string) Manager {
	return &manager{vaultRoot: vaultRoot}
}

func (m *manager) Run(_ context.Context, operation model.Operation) (model.Report, error) {
	configurationPath := filepath.Join(m.vaultRoot, ".obsidian")
	info, err := os.Stat(configurationPath)
	if err != nil {
		return model.Report{}, &model.VaultValidationError{Path: configurationPath, Reason: err.Error()}
	}
	if !info.IsDir() {
		return model.Report{}, &model.VaultValidationError{Path: configurationPath, Reason: "not a directory"}
	}
	plugins, err := vault.Inspect(m.vaultRoot)
	if err != nil {
		return model.Report{}, err
	}
	if operation.List.EnabledOnly {
		filtered := make([]model.PluginObservation, 0, len(plugins))
		for _, plugin := range plugins {
			if plugin.Enabled != nil && *plugin.Enabled {
				filtered = append(filtered, plugin)
			}
		}
		plugins = filtered
	}
	return model.Report{SchemaVersion: 1, Plugins: plugins}, nil
}
