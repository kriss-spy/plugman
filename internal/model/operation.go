package model

import "fmt"

// OperationKind identifies one command in the closed operation set.
type OperationKind string

const (
	// OperationList inspects Community Plugin state without network access.
	OperationList OperationKind = "list"
	// OperationInfo resolves one Plugin Input without mutating the Vault.
	OperationInfo OperationKind = "info"
	// OperationInstall plans or applies one or more Plugin Inputs.
	OperationInstall OperationKind = "install"
	// OperationUpdate advances selected installed plugins.
	OperationUpdate    OperationKind = "update"
	OperationOutdated  OperationKind = "outdated"
	OperationUninstall OperationKind = "uninstall"
	OperationExport    OperationKind = "export"
)

// Operation is a request handled by the package Manager.
type Operation struct {
	Kind      OperationKind
	List      ListOptions
	Info      InfoOptions
	Install   InstallOptions
	Update    UpdateOptions
	Outdated  OutdatedOptions
	Uninstall UninstallOptions
	Export    ExportOptions
}

// UpdateOptions configures a targeted update operation.
type UpdateOptions struct {
	Inputs          []string
	ObsidianVersion string
	AllowDowngrade  bool
	DryRun          bool
}

type OutdatedOptions struct {
	ObsidianVersion string
}

type UninstallOptions struct {
	IDs      []string
	KeepData bool
	Yes      bool
	DryRun   bool
}

type ExportOptions struct {
	Path            string
	EnabledOnly     bool
	Latest          bool
	Force           bool
	ObsidianVersion string
}

// InfoOptions configures an info operation.
type InfoOptions struct {
	Input           string
	ObsidianVersion string
}

// InstallOptions configures installation planning and application.
type InstallOptions struct {
	Inputs          []string
	ObsidianVersion string
	Enable          bool
	AllowDowngrade  bool
	DryRun          bool
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
	SourceOfficial SourceKind = "official"
	SourceUnknown  SourceKind = "unknown"
	SourceGitHub   SourceKind = "github"
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
	// HasData is an internal Vault snapshot fact and is not part of list JSON.
	HasData *bool `json:"-"`
}

// PendingRecoveryError prevents read-only commands from observing a
// transitional Plugin State. A later non-dry mutation performs recovery first.
type PendingRecoveryError struct{ Path string }

func (e *PendingRecoveryError) Error() string {
	return fmt.Sprintf("Vault has pending plugin recovery at %s", e.Path)
}

// InstallationState summarizes whether a resolved plugin is present locally.
type InstallationState string

const (
	NotInstalled   InstallationState = "not-installed"
	Installed      InstallationState = "installed"
	InstallInvalid InstallationState = "invalid"
)

// PluginInfo combines remote release metadata with current Vault state.
type PluginInfo struct {
	ID                      string            `json:"id"`
	Name                    string            `json:"name"`
	Author                  string            `json:"author,omitempty"`
	Description             string            `json:"description,omitempty"`
	Source                  PluginSource      `json:"source"`
	Installation            InstallationState `json:"installation"`
	InstalledVersion        *string           `json:"installedVersion"`
	Enabled                 *bool             `json:"enabled"`
	NewestCompatibleVersion string            `json:"newestCompatibleVersion"`
	MinimumObsidianVersion  string            `json:"minimumObsidianVersion"`
	DesktopOnly             bool              `json:"desktopOnly"`
	ReleaseURL              string            `json:"releaseUrl"`
}

// PlanAction identifies one deterministic proposed mutation.
type PlanAction string

const (
	PlanInstall   PlanAction = "install"
	PlanUpgrade   PlanAction = "upgrade"
	PlanDowngrade PlanAction = "downgrade"
	PlanUnchanged PlanAction = "unchanged"
	PlanUninstall PlanAction = "uninstall"
)

// PlannedPlugin is one resolved entry in an operation plan.
type PlannedPlugin struct {
	ID             string       `json:"id"`
	CurrentVersion *string      `json:"currentVersion"`
	TargetVersion  string       `json:"targetVersion"`
	Action         PlanAction   `json:"action"`
	Source         PluginSource `json:"source"`
}

// ResultCategory summarizes the mutation guarantees achieved by a command.
type ResultCategory string

const (
	ResultSuccess          ResultCategory = "success"
	ResultPreflightFailure ResultCategory = "preflight-failure"
	ResultPartialFailure   ResultCategory = "partial-failure"
	ResultRecoveryRequired ResultCategory = "recovery-required"
)

// ChangeResult describes the observable result for one planned plugin.
type ChangeResult struct {
	ID       string     `json:"id"`
	Action   PlanAction `json:"action"`
	Changed  bool       `json:"changed"`
	Restored bool       `json:"restored"`
	Error    string     `json:"error,omitempty"`
}

type OutdatedState string

const (
	OutdatedCurrent       OutdatedState = "current"
	OutdatedAvailable     OutdatedState = "outdated"
	OutdatedUnknownSource OutdatedState = "unknown-source"
	OutdatedRemoved       OutdatedState = "removed"
	OutdatedIncompatible  OutdatedState = "incompatible"
	OutdatedTransport     OutdatedState = "transport"
)

type OutdatedPlugin struct {
	ID             string        `json:"id"`
	CurrentVersion string        `json:"currentVersion"`
	LatestVersion  string        `json:"latestVersion,omitempty"`
	Enabled        bool          `json:"enabled"`
	Source         PluginSource  `json:"source"`
	ReleaseURL     string        `json:"releaseUrl,omitempty"`
	State          OutdatedState `json:"state"`
	Problem        string        `json:"problem,omitempty"`
}

type UninstallCandidate struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Enabled bool   `json:"enabled"`
	HasData bool   `json:"hasData"`
}

type ExportResult struct {
	Path    string `json:"path"`
	Written int    `json:"written"`
}

// Report is the terminal-independent result of an Operation.
type Report struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Plugins       []PluginObservation  `json:"plugins"`
	Info          *PluginInfo          `json:"info,omitempty"`
	Plan          []PlannedPlugin      `json:"plan,omitempty"`
	Results       []ChangeResult       `json:"results,omitempty"`
	Category      ResultCategory       `json:"category,omitempty"`
	Outdated      []OutdatedPlugin     `json:"outdated,omitempty"`
	Uninstall     []UninstallCandidate `json:"uninstall,omitempty"`
	Export        *ExportResult        `json:"export,omitempty"`
}

type ConfirmationRequiredError struct{}

func (*ConfirmationRequiredError) Error() string { return "uninstall requires confirmation" }

// VaultValidationError means the current directory is not an exact Vault Root.
type VaultValidationError struct {
	Path   string
	Reason string
}

func (e *VaultValidationError) Error() string {
	return fmt.Sprintf("current directory is not an Obsidian Vault Root: %s: %s", e.Path, e.Reason)
}
