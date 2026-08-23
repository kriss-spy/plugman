package manager

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/kriss-spy/plugman/internal/change"
	plugininput "github.com/kriss-spy/plugman/internal/input"
	"github.com/kriss-spy/plugman/internal/model"
	"github.com/kriss-spy/plugman/internal/obsidian"
	"github.com/kriss-spy/plugman/internal/pluginlist"
	"github.com/kriss-spy/plugman/internal/source"
	"github.com/kriss-spy/plugman/internal/stage"
	"github.com/kriss-spy/plugman/internal/status"
	"github.com/kriss-spy/plugman/internal/vault"
)

// Manager executes Plugman operations for one candidate Vault Root.
type Manager interface {
	Run(context.Context, model.Operation) (model.Report, error)
}

type manager struct {
	vaultRoot  string
	official   OfficialResolver
	recognizer OfficialRecognizer
	github     GitHubResolver
	target     TargetVersionProvider
	stager     ReleaseStager
	changer    ChangeEngine
	planReady  func(model.Report) error
	liveClient obsidian.Client
	session    func(obsidian.ChangePlan) change.RuntimeSession
}

// OfficialResolver is the remote seam used by info and official operations.
type OfficialResolver interface {
	Resolve(context.Context, string, source.Target) (source.Release, error)
	Lookup(context.Context, string) (source.OfficialPlugin, error)
	ResolveExact(context.Context, source.OfficialPlugin, string, source.Target) (source.Release, error)
}

// OfficialRecognizer checks directory membership without release selection.
type OfficialRecognizer interface {
	Recognize(context.Context, string) (bool, error)
}

// GitHubResolver resolves explicit repository and exact release URLs.
type GitHubResolver interface {
	Resolve(context.Context, string, source.Target) (source.Release, source.GitHubProvenance, error)
}

// TargetVersionProvider resolves the desktop version used for compatibility.
type TargetVersionProvider interface {
	Latest(context.Context) (string, error)
}

// ReleaseStager performs whole-batch download and asset validation.
type ReleaseStager interface {
	Preflight(context.Context, string, []source.Release) ([]stage.Prepared, error)
	PreflightDryRun(context.Context, string, []source.Release) ([]stage.Prepared, error)
}

// ChangeEngine applies one already-prepared plugin transition.
type ChangeEngine interface {
	BeginBatch(context.Context, string) (change.Batch, error)
	Validate(context.Context, change.PreparedChange) error
}

// Config injects external dependencies for deterministic callers and tests.
type Config struct {
	Official            OfficialResolver
	OfficialRecognition OfficialRecognizer
	GitHub              GitHubResolver
	TargetVersion       TargetVersionProvider
	Stager              ReleaseStager
	Change              ChangeEngine
	PlanReady           func(model.Report) error
	LiveClient          obsidian.Client
	LiveSession         func(obsidian.ChangePlan) change.RuntimeSession
}

// New creates a Manager rooted at the exact directory Plugman should manage.
func New(vaultRoot string) Manager {
	return NewWithConfig(vaultRoot, Config{})
}

// NewWithConfig creates a Manager with explicit external adapters.
func NewWithConfig(vaultRoot string, config Config) Manager {
	official := config.Official
	if official == nil {
		official = source.NewOfficialResolver(source.OfficialConfig{})
	}
	recognizer := config.OfficialRecognition
	if recognizer == nil {
		if value, ok := official.(OfficialRecognizer); ok {
			recognizer = value
		} else {
			recognizer = resolvingRecognizer{resolver: official}
		}
	}
	github := config.GitHub
	if github == nil {
		github = source.NewGitHubResolver(source.GitHubConfig{})
	}
	target := config.TargetVersion
	if target == nil {
		target = source.NewObsidianVersionProvider(nil, "")
	}
	stager := config.Stager
	if stager == nil {
		stager = stage.New(stage.Config{})
	}
	changer := config.Change
	if changer == nil {
		changer = change.New()
	}
	liveClient := config.LiveClient
	if liveClient == nil {
		liveClient = obsidian.NewCLIClient(obsidian.CLIConfig{VaultPath: vaultRoot})
	}
	session := config.LiveSession
	if session == nil {
		coordinator := obsidian.NewCoordinator(liveClient)
		session = func(plan obsidian.ChangePlan) change.RuntimeSession { return coordinator.Session(plan) }
	}
	return &manager{vaultRoot: vaultRoot, official: official, recognizer: recognizer, github: github, target: target, stager: stager, changer: changer, planReady: config.PlanReady, liveClient: liveClient, session: session}
}

type resolvingRecognizer struct{ resolver OfficialResolver }

func (r resolvingRecognizer) Recognize(ctx context.Context, id string) (bool, error) {
	_, err := r.resolver.Resolve(ctx, id, source.Target{ObsidianVersion: "9999.0.0"})
	if officialNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (m *manager) Run(ctx context.Context, operation model.Operation) (model.Report, error) {
	if err := vault.ValidateRoot(m.vaultRoot); err != nil {
		return model.Report{}, err
	}
	var batch change.Batch
	var err error
	if mutatesVault(operation) {
		batch, err = m.changer.BeginBatch(ctx, m.vaultRoot)
		if err != nil {
			return model.Report{SchemaVersion: 1, Category: model.ResultPreflightFailure}, err
		}
		defer batch.Close()
		if _, err := batch.Recover(ctx); err != nil {
			category := model.ResultPreflightFailure
			var recoveryRequired *change.RecoveryRequiredError
			if errors.As(err, &recoveryRequired) {
				category = model.ResultRecoveryRequired
			}
			return model.Report{SchemaVersion: 1, Category: category}, err
		}
	}
	plugins, err := vault.Inspect(m.vaultRoot)
	if err != nil {
		return model.Report{}, err
	}
	switch operation.Kind {
	case model.OperationList:
		return listReport(plugins, operation.List), nil
	case model.OperationInfo:
		return m.info(ctx, plugins, operation.Info)
	case model.OperationInstall:
		return m.install(ctx, batch, plugins, operation.Install)
	case model.OperationUpdate:
		return m.update(ctx, batch, plugins, operation.Update)
	case model.OperationOutdated:
		return m.outdated(ctx, plugins, operation.Outdated)
	case model.OperationUninstall:
		return m.uninstall(ctx, batch, plugins, operation.Uninstall)
	case model.OperationExport:
		return m.export(ctx, plugins, operation.Export)
	default:
		return model.Report{}, fmt.Errorf("unsupported operation %q", operation.Kind)
	}
}

func mutatesVault(operation model.Operation) bool {
	switch operation.Kind {
	case model.OperationInstall:
		return !operation.Install.DryRun
	case model.OperationUpdate:
		return !operation.Update.DryRun
	case model.OperationUninstall:
		return !operation.Uninstall.DryRun
	default:
		return false
	}
}

func (m *manager) install(ctx context.Context, batch change.Batch, plugins []model.PluginObservation, options model.InstallOptions) (model.Report, error) {
	if len(options.Inputs) == 0 {
		return preflightReport(model.Report{}, plugins, fmt.Errorf("install requires at least one Plugin Input"))
	}
	report, err := m.mutate(ctx, batch, plugins, mutationOptions{
		inputs: options.Inputs, obsidianVersion: options.ObsidianVersion, enable: options.Enable,
		allowDowngrade: options.AllowDowngrade, dryRun: options.DryRun,
	})
	return preflightReport(report, plugins, err)
}

func (m *manager) update(ctx context.Context, batch change.Batch, plugins []model.PluginObservation, options model.UpdateOptions) (model.Report, error) {
	if len(options.Inputs) == 0 {
		inputs, err := m.officialInstalledIDs(ctx, plugins)
		if err != nil {
			return preflightReport(model.Report{}, plugins, err)
		}
		options.Inputs = inputs
	}
	report, err := m.mutate(ctx, batch, plugins, mutationOptions{
		inputs: options.Inputs, obsidianVersion: options.ObsidianVersion, update: true,
		allowDowngrade: options.AllowDowngrade, dryRun: options.DryRun,
	})
	return preflightReport(report, plugins, err)
}

func (m *manager) officialInstalledIDs(ctx context.Context, plugins []model.PluginObservation) ([]string, error) {
	ids := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		if plugin.Status != model.PluginValid || plugin.ID == nil {
			continue
		}
		recognized, recognizeErr := m.recognizer.Recognize(ctx, *plugin.ID)
		if recognizeErr != nil {
			return nil, recognizeErr
		}
		if recognized {
			ids = append(ids, *plugin.ID)
		}
	}
	return ids, nil
}

func preflightReport(report model.Report, plugins []model.PluginObservation, err error) (model.Report, error) {
	if err != nil && report.Category == "" {
		report.SchemaVersion = 1
		report.Plugins = plugins
		report.Category = model.ResultPreflightFailure
	}
	return report, err
}

func (m *manager) outdated(ctx context.Context, plugins []model.PluginObservation, options model.OutdatedOptions) (model.Report, error) {
	hasCheckable := false
	for _, plugin := range plugins {
		if plugin.Status == model.PluginValid && plugin.ID != nil && plugin.Version != nil && plugin.Enabled != nil {
			hasCheckable = true
			break
		}
	}
	if !hasCheckable {
		return model.Report{SchemaVersion: 1, Plugins: plugins, Outdated: []model.OutdatedPlugin{}}, nil
	}
	targetVersion, err := m.compatibilityTarget(ctx, options.ObsidianVersion)
	if err != nil {
		return model.Report{}, err
	}
	resolver := &statusResolver{manager: m, target: source.Target{ObsidianVersion: targetVersion}, cached: make(map[string]statusResolution)}
	installed := make([]status.Installed, 0, len(plugins))
	var registryFailure error
	for _, plugin := range plugins {
		if plugin.Status != model.PluginValid || plugin.ID == nil || plugin.Version == nil || plugin.Enabled == nil {
			continue
		}
		entry := status.Installed{ID: *plugin.ID, Version: *plugin.Version, Enabled: *plugin.Enabled, Source: statusSource(plugin.Source)}
		recognized := false
		if registryFailure == nil {
			var recognizeErr error
			recognized, recognizeErr = m.recognizer.Recognize(ctx, entry.ID)
			if recognizeErr != nil {
				registryFailure = recognizeErr
			}
		}
		if recognized {
			entry.Source = status.Source{Kind: status.SourceOfficial}
		} else if registryFailure != nil && entry.Source.Kind != status.SourceGitHub {
			// Status.Check resolves known sources. Treat a local plugin whose
			// official identity could not be checked as a transport result, and
			// cache the registry failure so it is not fetched again per plugin.
			entry.Source = status.Source{Kind: status.SourceOfficial}
			resolver.cached[entry.ID] = statusResolution{err: registryFailure}
		}
		installed = append(installed, entry)
	}
	checked := status.Check(ctx, installed, resolver)
	result := make([]model.OutdatedPlugin, len(checked))
	for index, item := range checked {
		result[index] = model.OutdatedPlugin{
			ID: item.ID, CurrentVersion: item.CurrentVersion, LatestVersion: item.LatestVersion,
			Enabled: item.Enabled, Source: modelSource(item.Source), ReleaseURL: item.ReleaseURL,
			State: model.OutdatedState(item.State), Problem: item.Problem,
		}
	}
	return model.Report{SchemaVersion: 1, Plugins: plugins, Outdated: result}, nil
}

type statusResolution struct {
	release source.Release
	err     error
}

type statusResolver struct {
	manager *manager
	target  source.Target
	cached  map[string]statusResolution
}

func (r *statusResolver) ResolveLatest(ctx context.Context, plugin status.Installed) (status.Release, error) {
	resolved, ok := r.cached[plugin.ID]
	if !ok {
		if plugin.Source.Kind == status.SourceGitHub {
			release, _, err := r.manager.github.Resolve(ctx, plugin.Source.Repository, r.target)
			resolved = statusResolution{release: release, err: err}
		} else {
			release, err := r.manager.official.Resolve(ctx, plugin.ID, r.target)
			resolved = statusResolution{release: release, err: err}
		}
		r.cached[plugin.ID] = resolved
	}
	if resolved.err != nil {
		return status.Release{}, statusError(resolved.err)
	}
	return status.Release{Version: resolved.release.Version, URL: resolved.release.ReleaseURL}, nil
}

func statusError(err error) error {
	var sourceError *source.Error
	if !errors.As(err, &sourceError) {
		return err
	}
	switch sourceError.Code {
	case source.ErrorOfficialPluginNotFound:
		return &status.ResolveError{Kind: status.ResolveRemoved, Message: err.Error(), Err: err}
	case source.ErrorNoCompatibleRelease:
		return &status.ResolveError{Kind: status.ResolveIncompatible, Message: err.Error(), Err: err}
	default:
		return err
	}
}

func statusSource(pluginSource model.PluginSource) status.Source {
	result := status.Source{Kind: status.SourceUnknown}
	if pluginSource.Kind == model.SourceGitHub && pluginSource.Repository != nil {
		result.Kind, result.Repository = status.SourceGitHub, *pluginSource.Repository
		if pluginSource.Release != nil {
			result.Release = *pluginSource.Release
		}
	} else if pluginSource.Kind == model.SourceOfficial {
		result.Kind = status.SourceOfficial
	}
	return result
}

func modelSource(value status.Source) model.PluginSource {
	result := model.PluginSource{Kind: model.SourceUnknown}
	switch value.Kind {
	case status.SourceOfficial:
		result.Kind = model.SourceOfficial
	case status.SourceGitHub:
		result.Kind = model.SourceGitHub
		result.Repository = &value.Repository
		result.Release = &value.Release
	}
	return result
}

func (m *manager) export(ctx context.Context, plugins []model.PluginObservation, options model.ExportOptions) (model.Report, error) {
	if options.Path == "" {
		return model.Report{}, fmt.Errorf("export requires a Plugin List path")
	}
	entries := make([]pluginlist.Plugin, 0, len(plugins))
	for _, plugin := range plugins {
		if options.EnabledOnly && plugin.Enabled != nil && !*plugin.Enabled {
			continue
		}
		entry := pluginlist.Plugin{ID: plugin.Folder, Source: pluginlist.Source{Kind: pluginlist.SourceUnknown}}
		if plugin.ID != nil {
			entry.ID = *plugin.ID
		}
		if plugin.Version != nil {
			entry.Version = *plugin.Version
		}
		if plugin.Enabled != nil {
			entry.Enabled = *plugin.Enabled
		}
		for _, problem := range plugin.Problems {
			entry.Problems = append(entry.Problems, problem.Code)
		}
		if plugin.Status == model.PluginValid && plugin.ID != nil {
			recognized, recognizeErr := m.recognizer.Recognize(ctx, *plugin.ID)
			if recognizeErr != nil {
				return model.Report{}, recognizeErr
			}
			if recognized {
				entry.Source.Kind = pluginlist.SourceOfficial
			} else if plugin.Source.Kind == model.SourceGitHub && plugin.Source.Repository != nil && plugin.Source.Release != nil {
				entry.Source = pluginlist.Source{Kind: pluginlist.SourceGitHub, Repository: *plugin.Source.Repository, Release: *plugin.Source.Release}
			}
		}
		entries = append(entries, entry)
	}
	destination := options.Path
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(m.vaultRoot, destination)
	}
	exported, err := pluginlist.Export(destination, entries, pluginlist.Options{EnabledOnly: options.EnabledOnly, Latest: options.Latest, Force: options.Force})
	if err != nil {
		return model.Report{}, err
	}
	return model.Report{SchemaVersion: 1, Plugins: plugins, Export: &model.ExportResult{Path: exported.Path, Written: exported.Written}}, nil
}

func (m *manager) uninstall(ctx context.Context, batch change.Batch, plugins []model.PluginObservation, options model.UninstallOptions) (model.Report, error) {
	if len(options.IDs) == 0 {
		return preflightReport(model.Report{}, plugins, fmt.Errorf("uninstall requires at least one plugin ID"))
	}
	installed := make(map[string]model.PluginObservation, len(plugins))
	for _, plugin := range plugins {
		if plugin.ID != nil {
			installed[*plugin.ID] = plugin
		}
	}
	seen := make(map[string]bool, len(options.IDs))
	var candidates []model.UninstallCandidate
	var plan []model.PlannedPlugin
	for _, id := range options.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		plugin, ok := installed[id]
		if !ok || plugin.Status != model.PluginValid || plugin.Version == nil || plugin.Enabled == nil {
			return preflightReport(model.Report{}, plugins, fmt.Errorf("cannot uninstall %q: plugin is not installed with valid Plugin State", id))
		}
		if plugin.HasData == nil {
			return preflightReport(model.Report{}, plugins, fmt.Errorf("cannot uninstall %q: data presence is unavailable", id))
		}
		candidates = append(candidates, model.UninstallCandidate{ID: id, Version: *plugin.Version, Enabled: *plugin.Enabled, HasData: *plugin.HasData})
		plan = append(plan, model.PlannedPlugin{ID: id, CurrentVersion: plugin.Version, Action: model.PlanUninstall})
	}
	report := model.Report{SchemaVersion: 1, Plugins: plugins, Plan: plan, Uninstall: candidates, Category: model.ResultSuccess}
	if !options.DryRun && !options.Yes {
		return report, &model.ConfirmationRequiredError{}
	}
	changes := make([]change.PreparedChange, len(candidates))
	for index, candidate := range candidates {
		changes[index] = change.PreparedChange{VaultRoot: m.vaultRoot, PluginID: candidate.ID, Kind: change.Uninstall, Enabled: change.Disable, KeepData: options.KeepData}
		var validateErr error
		if options.DryRun {
			validateErr = m.changer.Validate(ctx, changes[index])
		} else {
			validateErr = batch.Validate(ctx, changes[index])
		}
		if validateErr != nil {
			report.Category = model.ResultPreflightFailure
			return report, validateErr
		}
	}
	runtimeChanges := make([]runtimeChange, len(candidates))
	for index := range candidates {
		runtimeChanges[index] = runtimeChange{
			id: candidates[index].ID, currentVersion: plan[index].CurrentVersion,
			currentEnabled: &candidates[index].Enabled, targetAbsent: true,
		}
	}
	live, runtimeStates, err := m.prepareRuntime(ctx, runtimeChanges)
	if err != nil {
		report.Category = model.ResultPreflightFailure
		return report, err
	}
	if m.planReady != nil {
		if err := m.planReady(report); err != nil {
			report.Category = model.ResultPreflightFailure
			return report, err
		}
	}
	if options.DryRun {
		return report, nil
	}
	for index, prepared := range changes {
		var outcome change.Outcome
		var applyErr error
		if live {
			outcome, applyErr = batch.ApplyLive(ctx, prepared, m.session(obsidian.ChangePlan{
				PluginID: prepared.PluginID, PlannedState: runtimeStates[index], TargetAbsent: true,
			}))
		} else {
			outcome, applyErr = batch.Apply(ctx, prepared)
		}
		result := model.ChangeResult{ID: prepared.PluginID, Action: model.PlanUninstall, Changed: outcome.Changed, Restored: outcome.Restored}
		if applyErr != nil {
			result.Error = applyErr.Error()
			report.Results = append(report.Results, result)
			var recoveryRequired *change.RecoveryRequiredError
			if errors.As(applyErr, &recoveryRequired) {
				report.Category = model.ResultRecoveryRequired
			} else if index > 0 {
				report.Category = model.ResultPartialFailure
			} else {
				report.Category = model.ResultPreflightFailure
			}
			return report, applyErr
		}
		report.Results = append(report.Results, result)
	}
	return report, nil
}

func (m *manager) compatibilityTarget(ctx context.Context, value string) (string, error) {
	if value != "" {
		return value, nil
	}
	return m.target.Latest(ctx)
}

func officialNotFound(err error) bool {
	var sourceError *source.Error
	return errors.As(err, &sourceError) && sourceError.Code == source.ErrorOfficialPluginNotFound
}

type mutationOptions struct {
	inputs          []string
	obsidianVersion string
	enable          bool
	update          bool
	allowDowngrade  bool
	dryRun          bool
}

type resolvedPlan struct {
	planned     model.PlannedPlugin
	release     source.Release
	declaration plugininput.Declaration
}

type plannedMutation struct {
	planned     model.PlannedPlugin
	release     source.Release
	reportIndex int
}

func (m *manager) mutate(ctx context.Context, batch change.Batch, plugins []model.PluginObservation, options mutationOptions) (model.Report, error) {
	declarations, err := plugininput.Expand(m.vaultRoot, options.inputs)
	if err != nil {
		return model.Report{}, err
	}
	if len(declarations) == 0 {
		report := model.Report{SchemaVersion: 1, Plugins: plugins, Plan: []model.PlannedPlugin{}, Category: model.ResultSuccess}
		if m.planReady != nil {
			if err := m.planReady(report); err != nil {
				return report, err
			}
		}
		return report, nil
	}
	if options.obsidianVersion == "" {
		options.obsidianVersion, err = m.target.Latest(ctx)
		if err != nil {
			return model.Report{}, err
		}
	}
	installed := make(map[string]model.PluginObservation, len(plugins))
	for _, plugin := range plugins {
		if plugin.ID != nil {
			installed[*plugin.ID] = plugin
		}
	}
	resolved := make([]resolvedPlan, 0, len(declarations))
	resolvedIndexes := make(map[string]int, len(declarations))
	for _, declaration := range declarations {
		release, pluginSource, resolveErr := m.resolveDeclaration(ctx, declaration, options.obsidianVersion)
		if resolveErr != nil {
			return model.Report{}, resolveErr
		}
		if index, duplicate := resolvedIndexes[release.PluginID]; duplicate {
			existing := resolved[index]
			if existing.planned.TargetVersion == release.Version {
				if pluginSource.Kind == model.SourceGitHub && existing.planned.Source.Kind != model.SourceGitHub {
					existing.planned.Source = pluginSource
					existing.release = release
					existing.declaration = declaration
					resolved[index] = existing
				}
				continue
			}
			existingExact, incomingExact := exactDeclaration(existing.declaration), exactDeclaration(declaration)
			if existingExact && incomingExact {
				return model.Report{}, fmt.Errorf("plugin %q resolves to conflicting exact versions %s and %s", release.PluginID, resolved[index].planned.TargetVersion, release.Version)
			}
			if !existingExact && incomingExact {
				entry, planErr := planRelease(release, pluginSource, declaration, installed, options)
				if planErr != nil {
					return model.Report{}, planErr
				}
				resolved[index] = resolvedPlan{planned: entry, release: release, declaration: declaration}
			}
			continue
		}
		entry, planErr := planRelease(release, pluginSource, declaration, installed, options)
		if planErr != nil {
			return model.Report{}, planErr
		}
		resolvedIndexes[release.PluginID] = len(resolved)
		resolved = append(resolved, resolvedPlan{planned: entry, release: release, declaration: declaration})
	}
	plan := make([]model.PlannedPlugin, len(resolved))
	for index := range resolved {
		plan[index] = resolved[index].planned
	}
	report := model.Report{SchemaVersion: 1, Plugins: plugins, Plan: plan, Category: model.ResultSuccess}
	changes := make([]plannedMutation, 0, len(resolved))
	releases := make([]source.Release, 0, len(resolved))
	results := make([]model.ChangeResult, len(resolved))
	for reportIndex, item := range resolved {
		if item.planned.Action == model.PlanUnchanged {
			results[reportIndex] = model.ChangeResult{ID: item.planned.ID, Action: item.planned.Action}
			continue
		}
		changes = append(changes, plannedMutation{planned: item.planned, release: item.release, reportIndex: reportIndex})
		releases = append(releases, item.release)
	}
	var prepared []stage.Prepared
	if options.dryRun {
		prepared, err = m.stager.PreflightDryRun(ctx, m.vaultRoot, releases)
	} else {
		prepared, err = m.stager.Preflight(ctx, m.vaultRoot, releases)
	}
	if err != nil {
		report.Category = model.ResultPreflightFailure
		return report, err
	}
	if len(prepared) != len(changes) {
		cleanupPrepared(prepared)
		report.Category = model.ResultPreflightFailure
		return report, fmt.Errorf("stager returned %d plugins for %d releases", len(prepared), len(changes))
	}
	defer cleanupPrepared(prepared)
	preparedChanges := make([]change.PreparedChange, len(changes))
	for index := range changes {
		item := changes[index]
		kind := change.Install
		if item.planned.Action != model.PlanInstall {
			kind = change.Update
		}
		enabled := change.PreserveEnabled
		if kind == change.Install && options.enable {
			enabled = change.Enable
		}
		preparedChanges[index] = change.PreparedChange{
			VaultRoot: m.vaultRoot, PluginID: item.planned.ID, Kind: kind,
			StagedDir: prepared[index].Dir, Enabled: enabled, SourceRecord: sourceRecord(item.planned.Source),
		}
	}
	for _, preparedChange := range preparedChanges {
		var validateErr error
		if options.dryRun {
			validateErr = m.changer.Validate(ctx, preparedChange)
		} else {
			validateErr = batch.Validate(ctx, preparedChange)
		}
		if validateErr != nil {
			report.Category = model.ResultPreflightFailure
			return report, validateErr
		}
	}
	runtimeChanges := make([]runtimeChange, len(changes))
	for index, item := range changes {
		runtimeChanges[index] = runtimeChange{
			id: item.planned.ID, currentVersion: item.planned.CurrentVersion,
			targetVersion: item.planned.TargetVersion, enableNew: options.enable && item.planned.Action == model.PlanInstall,
		}
		if current, ok := installed[item.planned.ID]; ok {
			runtimeChanges[index].currentEnabled = current.Enabled
		}
	}
	live, runtimeStates, err := m.prepareRuntime(ctx, runtimeChanges)
	if err != nil {
		report.Category = model.ResultPreflightFailure
		return report, err
	}
	if m.planReady != nil {
		if err := m.planReady(report); err != nil {
			report.Category = model.ResultPreflightFailure
			return report, err
		}
	}
	if options.dryRun {
		return report, nil
	}

	for index, item := range changes {
		var outcome change.Outcome
		var applyErr error
		if live {
			outcome, applyErr = batch.ApplyLive(ctx, preparedChanges[index], m.session(obsidian.ChangePlan{
				PluginID: item.planned.ID, PlannedState: runtimeStates[index], TargetVersion: item.planned.TargetVersion,
				EnableNew: runtimeChanges[index].enableNew,
			}))
		} else {
			outcome, applyErr = batch.Apply(ctx, preparedChanges[index])
		}
		result := model.ChangeResult{ID: item.planned.ID, Action: item.planned.Action, Changed: outcome.Changed, Restored: outcome.Restored}
		results[item.reportIndex] = result
		if applyErr != nil {
			result.Error = applyErr.Error()
			results[item.reportIndex] = result
			report.Results = completedResults(results)
			var recoveryRequired *change.RecoveryRequiredError
			if errors.As(applyErr, &recoveryRequired) {
				report.Category = model.ResultRecoveryRequired
			} else if anyChanged(results[:item.reportIndex]) {
				report.Category = model.ResultPartialFailure
			} else {
				report.Category = model.ResultPreflightFailure
			}
			return report, applyErr
		}
	}
	report.Results = completedResults(results)
	return report, nil
}

type runtimeChange struct {
	id             string
	currentVersion *string
	currentEnabled *bool
	targetVersion  string
	enableNew      bool
	targetAbsent   bool
}

func (m *manager) prepareRuntime(ctx context.Context, changes []runtimeChange) (bool, []obsidian.PluginState, error) {
	if len(changes) == 0 {
		return false, nil, nil
	}
	if err := m.liveClient.Probe(ctx); err != nil {
		if errors.Is(err, obsidian.ErrObsidianNotRunning) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("cannot safely coordinate running Obsidian: %w", err)
	}
	states := make([]obsidian.PluginState, len(changes))
	for index, planned := range changes {
		state, err := m.liveClient.Inspect(ctx, planned.id)
		if err != nil {
			return false, nil, fmt.Errorf("inspect running plugin %q: %w", planned.id, err)
		}
		if planned.currentVersion == nil {
			if state != (obsidian.PluginState{}) {
				return false, nil, fmt.Errorf("plugin %q appeared in Obsidian after planning", planned.id)
			}
		} else if !state.Present || state.ID != planned.id || state.Version != *planned.currentVersion || planned.currentEnabled == nil || state.Enabled != *planned.currentEnabled {
			return false, nil, fmt.Errorf("plugin %q runtime state does not match planned Plugin State", planned.id)
		}
		states[index] = state
	}
	return true, states, nil
}

func anyChanged(results []model.ChangeResult) bool {
	for _, result := range results {
		if result.Changed {
			return true
		}
	}
	return false
}

func planRelease(release source.Release, pluginSource model.PluginSource, declaration plugininput.Declaration, installed map[string]model.PluginObservation, options mutationOptions) (model.PlannedPlugin, error) {
	entry := model.PlannedPlugin{ID: release.PluginID, TargetVersion: release.Version, Action: model.PlanInstall, Source: pluginSource}
	current, exists := installed[release.PluginID]
	if options.update && !exists {
		return model.PlannedPlugin{}, fmt.Errorf("cannot update %q: plugin is not installed", release.PluginID)
	}
	if !exists {
		return entry, nil
	}
	if current.Status != model.PluginValid || current.Version == nil {
		return model.PlannedPlugin{}, fmt.Errorf("installed plugin %q has invalid Plugin State", release.PluginID)
	}
	entry.CurrentVersion = current.Version
	exact := exactDeclaration(declaration)
	if !options.update && !exact {
		entry.TargetVersion = *current.Version
		entry.Action = model.PlanUnchanged
		return entry, nil
	}
	if *current.Version == release.Version {
		entry.Action = model.PlanUnchanged
		return entry, nil
	}
	comparison, err := source.CompareVersions(release.Version, *current.Version)
	if err != nil {
		return model.PlannedPlugin{}, fmt.Errorf("compare installed plugin %q: %w", release.PluginID, err)
	}
	if comparison > 0 {
		entry.Action = model.PlanUpgrade
		return entry, nil
	}
	if !options.allowDowngrade {
		return model.PlannedPlugin{}, fmt.Errorf("%s %s -> %s is a downgrade; pass --allow-downgrade", release.PluginID, *current.Version, release.Version)
	}
	entry.Action = model.PlanDowngrade
	return entry, nil
}

func exactDeclaration(declaration plugininput.Declaration) bool {
	return declaration.Version != "" || declaration.Kind == plugininput.GitHubRelease
}

func completedResults(results []model.ChangeResult) []model.ChangeResult {
	completed := make([]model.ChangeResult, 0, len(results))
	for _, result := range results {
		if result.ID != "" {
			completed = append(completed, result)
		}
	}
	return completed
}

func cleanupPrepared(prepared []stage.Prepared) {
	for _, item := range prepared {
		_ = item.Cleanup()
	}
}

func sourceRecord(pluginSource model.PluginSource) *change.SourceRecord {
	if pluginSource.Kind != model.SourceGitHub {
		return nil
	}
	if pluginSource.Repository == nil || pluginSource.Release == nil {
		return &change.SourceRecord{}
	}
	return &change.SourceRecord{Repository: *pluginSource.Repository, Release: *pluginSource.Release}
}

func listReport(plugins []model.PluginObservation, options model.ListOptions) model.Report {
	if options.EnabledOnly {
		filtered := make([]model.PluginObservation, 0, len(plugins))
		for _, plugin := range plugins {
			if plugin.Enabled != nil && *plugin.Enabled {
				filtered = append(filtered, plugin)
			}
		}
		plugins = filtered
	}
	return model.Report{SchemaVersion: 1, Plugins: plugins}
}

func (m *manager) info(ctx context.Context, plugins []model.PluginObservation, options model.InfoOptions) (model.Report, error) {
	declarations, err := plugininput.Expand(m.vaultRoot, []string{options.Input})
	if err != nil {
		return model.Report{}, err
	}
	if len(declarations) != 1 {
		return model.Report{}, fmt.Errorf("info requires exactly one Plugin Input")
	}
	if options.ObsidianVersion == "" {
		options.ObsidianVersion, err = m.target.Latest(ctx)
		if err != nil {
			return model.Report{}, err
		}
	}
	release, pluginSource, err := m.resolveDeclaration(ctx, declarations[0], options.ObsidianVersion)
	if err != nil {
		return model.Report{}, err
	}
	info := model.PluginInfo{
		ID:                      release.PluginID,
		Name:                    release.ReleaseManifest.Name,
		Author:                  release.ReleaseManifest.Author,
		Description:             release.ReleaseManifest.Description,
		Source:                  pluginSource,
		Installation:            model.NotInstalled,
		NewestCompatibleVersion: release.Version,
		MinimumObsidianVersion:  release.MinimumObsidianVersion,
		DesktopOnly:             release.DesktopOnly,
		ReleaseURL:              release.ReleaseURL,
	}
	for _, plugin := range plugins {
		if plugin.ID == nil || *plugin.ID != release.PluginID {
			continue
		}
		info.InstalledVersion = plugin.Version
		info.Enabled = plugin.Enabled
		if plugin.Status == model.PluginValid {
			info.Installation = model.Installed
		} else {
			info.Installation = model.InstallInvalid
		}
		break
	}
	return model.Report{SchemaVersion: 1, Plugins: plugins, Info: &info}, nil
}

func (m *manager) resolveDeclaration(ctx context.Context, declaration plugininput.Declaration, obsidianVersion string) (source.Release, model.PluginSource, error) {
	target := source.Target{ObsidianVersion: obsidianVersion}
	if declaration.Kind == plugininput.Official {
		var release source.Release
		var err error
		if declaration.Version == "" {
			release, err = m.official.Resolve(ctx, declaration.ID, target)
		} else {
			plugin, lookupErr := m.official.Lookup(ctx, declaration.ID)
			if lookupErr != nil {
				return source.Release{}, model.PluginSource{}, lookupErr
			}
			release, err = m.official.ResolveExact(ctx, plugin, declaration.Version, target)
		}
		if err != nil {
			return source.Release{}, model.PluginSource{}, err
		}
		repository := "https://github.com/" + release.Repository
		releaseURL := release.ReleaseURL
		return release, model.PluginSource{Kind: model.SourceOfficial, Repository: &repository, Release: &releaseURL}, nil
	}
	inputURL := declaration.Repository
	if declaration.Kind == plugininput.GitHubRelease {
		inputURL += "/releases/tag/" + declaration.Release
	}
	release, provenance, err := m.github.Resolve(ctx, inputURL, target)
	if err != nil {
		return source.Release{}, model.PluginSource{}, err
	}
	releaseValue := provenance.Release
	return release, model.PluginSource{Kind: model.SourceGitHub, Repository: &provenance.Repository, Release: &releaseValue}, nil
}
