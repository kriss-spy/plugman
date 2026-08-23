package model

import "fmt"

// OperationKind identifies one command in the closed operation set.
type OperationKind string

const (
	// OperationList inspects Community Plugin state without network access.
	OperationList OperationKind = "list"
)

// Operation is a request handled by the package Manager.
type Operation struct {
	Kind OperationKind
	List ListOptions
}

// ListOptions configures a list operation.
type ListOptions struct {
	EnabledOnly bool
}

// PluginStatus says whether an observation is usable Plugin State.
type PluginStatus string

const (
	PluginValid   PluginStatus = "valid"
	PluginInvalid PluginStatus = "invalid"
)

// SourceKind identifies provenance which can be proven from Vault-local state.
type SourceKind string

const (
	SourceUnknown SourceKind = "unknown"
	SourceGitHub  SourceKind = "github"
)

// PluginSource is the known Vault-local provenance for a Community Plugin.
type PluginSource struct {
	Kind       SourceKind `json:"kind"`
	Repository *string    `json:"repository"`
	Release    *string    `json:"release"`
}

// Problem describes one stable reason an observed Plugin State is invalid.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// PluginObservation describes one folder found in the Vault plugin directory.
type PluginObservation struct {
	Folder   string       `json:"folder"`
	ID       *string      `json:"id"`
	Version  *string      `json:"version"`
	Enabled  *bool        `json:"enabled"`
	Source   PluginSource `json:"source"`
	Status   PluginStatus `json:"status"`
	Problems []Problem    `json:"problems"`
}

// Report is the terminal-independent result of an Operation.
type Report struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Plugins       []PluginObservation `json:"plugins"`
}

// VaultValidationError means the current directory is not an exact Vault Root.
type VaultValidationError struct {
	Path   string
	Reason string
}

func (e *VaultValidationError) Error() string {
	return fmt.Sprintf("current directory is not an Obsidian Vault Root: %s: %s", e.Path, e.Reason)
}
