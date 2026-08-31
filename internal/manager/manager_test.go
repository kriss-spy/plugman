package manager_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kriss-spy/plugman/internal/change"
	plugininput "github.com/kriss-spy/plugman/internal/input"
	"github.com/kriss-spy/plugman/internal/manager"
	"github.com/kriss-spy/plugman/internal/model"
	"github.com/kriss-spy/plugman/internal/obsidian"
	"github.com/kriss-spy/plugman/internal/source"
	"github.com/kriss-spy/plugman/internal/stage"
)

func TestListRejectsDirectoryWithoutObsidianConfiguration(t *testing.T) {
	vaultRoot := t.TempDir()

	_, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})

	var validationError *model.VaultValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("Run() error = %v, want VaultValidationError", err)
	}
	wantPath := filepath.Join(vaultRoot, ".obsidian")
	if validationError.Path != wantPath {
		t.Errorf("validation path = %q, want %q", validationError.Path, wantPath)
	}
}

func TestListRefusesPendingRecoveryState(t *testing.T) {
	vaultRoot := newVault(t)
	recoveryPath := filepath.Join(vaultRoot, ".obsidian", ".plugman", "recovery", "current")
	if err := os.MkdirAll(recoveryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	var pending *model.PendingRecoveryError
	if !errors.As(err, &pending) || pending.Path != recoveryPath {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestListReportsInstalledPluginState(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "dataview", "manifest.json"), `{"id":"dataview","version":"0.5.67"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "community-plugins.json"), `["dataview"]`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []model.PluginObservation{{
		Folder:   "dataview",
		ID:       pointer("dataview"),
		Version:  pointer("0.5.67"),
		Enabled:  pointer(true),
		Source:   model.PluginSource{Kind: model.SourceUnknown},
		Status:   model.PluginValid,
		Problems: []model.Problem{},
		HasData:  pointer(false),
	}}
	if !reflect.DeepEqual(report.Plugins, want) {
		t.Errorf("plugins = %#v, want %#v", report.Plugins, want)
	}
}

func TestInfoCombinesOfficialReleaseAndInstalledState(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "dataview", "manifest.json"), `{"id":"dataview","version":"0.5.67"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "community-plugins.json"), `["dataview"]`)
	resolver := &fakeOfficialResolver{release: source.Release{
		PluginID: "dataview", Repository: "blacksmithgu/obsidian-dataview", Version: "0.5.68",
		MinimumObsidianVersion: "1.6.0", DesktopOnly: false,
		ReleaseURL:      "https://github.com/blacksmithgu/obsidian-dataview/releases/tag/0.5.68",
		ReleaseManifest: source.Manifest{ID: "dataview", Name: "Dataview", Version: "0.5.68"},
	}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{Official: resolver}).Run(context.Background(), model.Operation{
		Kind: model.OperationInfo,
		Info: model.InfoOptions{Input: "dataview", ObsidianVersion: "1.8.0"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resolver.id != "dataview" || resolver.target.ObsidianVersion != "1.8.0" {
		t.Fatalf("resolver request = %q %+v", resolver.id, resolver.target)
	}
	if report.Info == nil || report.Info.Installation != model.Installed || report.Info.InstalledVersion == nil || *report.Info.InstalledVersion != "0.5.67" || report.Info.Enabled == nil || !*report.Info.Enabled {
		t.Fatalf("info = %#v", report.Info)
	}
	if report.Info.NewestCompatibleVersion != "0.5.68" || report.Info.Source.Kind != model.SourceOfficial {
		t.Fatalf("info = %#v", report.Info)
	}
}

func TestInfoUsesReadOnlyOfficialInspection(t *testing.T) {
	vaultRoot := newVault(t)
	base := &fakeOfficialResolver{release: officialRelease("example", "2.0.0")}
	resolver := &inspectingOfficialResolver{fakeOfficialResolver: base}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{Official: resolver}).Run(context.Background(), model.Operation{
		Kind: model.OperationInfo,
		Info: model.InfoOptions{Input: "example", ObsidianVersion: "1.8.0"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resolver.inspectCalls != 1 || base.calls != 0 {
		t.Fatalf("inspection calls = %d, install-grade resolve calls = %d", resolver.inspectCalls, base.calls)
	}
	if report.Info == nil || report.Info.NewestCompatibleVersion != "2.0.0" {
		t.Fatalf("info = %#v", report.Info)
	}
}

func TestInfoWarmsOfficialDirectoryWhileResolvingCompatibilityTarget(t *testing.T) {
	vaultRoot := newVault(t)
	started := make(chan string, 2)
	release := make(chan struct{})
	target := &blockingTargetVersion{started: started, release: release}
	recognizer := &blockingRecognizer{started: started, release: release}
	configured := manager.NewWithConfig(vaultRoot, manager.Config{
		Official:            &fakeOfficialResolver{release: officialRelease("example", "2.0.0")},
		OfficialRecognition: recognizer,
		TargetVersion:       target,
	})
	done := make(chan error, 1)
	go func() {
		_, err := configured.Run(context.Background(), model.Operation{Kind: model.OperationInfo, Info: model.InfoOptions{Input: "example"}})
		done <- err
	}()

	for range 2 {
		select {
		case <-started:
		case <-time.After(500 * time.Millisecond):
			close(release)
			t.Fatal("compatibility target and official directory loaded one-by-one")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestInfoValidatesVaultBeforeRemoteResolution(t *testing.T) {
	resolver := &fakeOfficialResolver{}
	_, err := manager.NewWithConfig(t.TempDir(), manager.Config{Official: resolver}).Run(context.Background(), model.Operation{
		Kind: model.OperationInfo, Info: model.InfoOptions{Input: "dataview", ObsidianVersion: "1.8.0"},
	})
	if err == nil || resolver.calls != 0 {
		t.Fatalf("err = %v, resolver calls = %d", err, resolver.calls)
	}
}

func TestInfoAcceptsGitHubReleaseURL(t *testing.T) {
	vaultRoot := newVault(t)
	github := &fakeGitHubResolver{
		release:    source.Release{PluginID: "opencode", Repository: "kriss-spy/obsidian-opencode", Version: "1.3.13", MinimumObsidianVersion: "1.6.0", ReleaseURL: "https://github.com/kriss-spy/obsidian-opencode/releases/tag/1.3.13", ReleaseManifest: source.Manifest{ID: "opencode", Name: "OpenCode", Version: "1.3.13"}},
		provenance: source.GitHubProvenance{Repository: "https://github.com/kriss-spy/obsidian-opencode", Release: "1.3.13"},
	}
	inputURL := "https://github.com/kriss-spy/obsidian-opencode/releases/tag/1.3.13"
	report, err := manager.NewWithConfig(vaultRoot, manager.Config{Official: &fakeOfficialResolver{}, GitHub: github}).Run(context.Background(), model.Operation{
		Kind: model.OperationInfo, Info: model.InfoOptions{Input: inputURL, ObsidianVersion: "1.8.0"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if github.input != inputURL || report.Info == nil || report.Info.Source.Kind != model.SourceGitHub || report.Info.NewestCompatibleVersion != "1.3.13" {
		t.Fatalf("input = %q, info = %#v", github.input, report.Info)
	}
}

func TestInstallDryRunComposesListsAndLeavesInstalledUnversionedPluginUnchanged(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "dataview", "manifest.json"), `{"id":"dataview","version":"0.5.67"}`)
	listPath := filepath.Join(vaultRoot, "base.plugins")
	writeFile(t, listPath, "# base\ndataview\nhomepage\n")
	resolver := &mapOfficialResolver{releases: map[string]source.Release{
		"dataview": officialRelease("dataview", "0.5.68"),
		"homepage": officialRelease("homepage", "1.4.3"),
	}}
	stager := &fakeStager{t: t}
	changer := &fakeChanger{}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: resolver, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{
		Kind:    model.OperationInstall,
		Install: model.InstallOptions{Inputs: []string{"base.plugins"}, ObsidianVersion: "1.8.0", DryRun: true},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Plan) != 2 || report.Plan[0].ID != "dataview" || report.Plan[0].Action != model.PlanUnchanged || report.Plan[1].ID != "homepage" || report.Plan[1].Action != model.PlanInstall {
		t.Fatalf("plan = %#v", report.Plan)
	}
	contents, readErr := os.ReadFile(listPath)
	if readErr != nil || string(contents) != "# base\ndataview\nhomepage\n" {
		t.Fatalf("Plugin List changed: %q, %v", contents, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(vaultRoot, ".obsidian", ".plugman")); !os.IsNotExist(statErr) {
		t.Fatalf("dry-run created Plugman state: %v", statErr)
	}
	if stager.dryCalls != 1 || changer.beginCalls != 0 {
		t.Fatalf("dry-run staging calls = %d, batch calls = %d", stager.dryCalls, changer.beginCalls)
	}
}

func TestInstallUsesInstallGradeOfficialResolution(t *testing.T) {
	vaultRoot := newVault(t)
	base := &fakeOfficialResolver{release: officialRelease("demo", "1.0.0")}
	resolver := &inspectingOfficialResolver{fakeOfficialResolver: base}
	stager := &fakeStager{t: t}

	_, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: resolver, Stager: stager, Change: &fakeChanger{},
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{
		Kind:    model.OperationInstall,
		Install: model.InstallOptions{Inputs: []string{"demo"}, ObsidianVersion: "1.8.0", DryRun: true},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if base.calls != 1 || resolver.inspectCalls != 0 {
		t.Fatalf("install-grade resolve calls = %d, read-only inspection calls = %d", base.calls, resolver.inspectCalls)
	}
}

func TestInstallDryRunHonorsExactVersionAndRequiresDowngradeConsent(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "manifest.json"), `{"id":"demo","version":"2.0.0"}`)
	official := &fakeOfficialResolver{release: officialRelease("demo", "3.0.0"), exactRelease: officialRelease("demo", "1.0.0")}
	github := &fakeGitHubResolver{
		release:    officialRelease("demo", "1.0.0"),
		provenance: source.GitHubProvenance{Repository: "https://github.com/owner/demo", Release: "1.0.0"},
	}
	stager := &fakeStager{t: t}
	changer := &fakeChanger{}
	operation := model.Operation{Kind: model.OperationInstall, Install: model.InstallOptions{
		Inputs: []string{"demo@1.0.0"}, ObsidianVersion: "1.8.0", DryRun: true,
	}}
	configured := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: official, GitHub: github, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	})
	if _, err := configured.Run(context.Background(), operation); err == nil || !strings.Contains(err.Error(), "--allow-downgrade") {
		t.Fatalf("Run() error = %v, want downgrade consent error", err)
	}
	operation.Install.AllowDowngrade = true
	report, err := configured.Run(context.Background(), operation)
	if err != nil {
		t.Fatalf("Run() with consent error = %v", err)
	}
	if len(report.Plan) != 1 || report.Plan[0].Action != model.PlanDowngrade || report.Plan[0].TargetVersion != "1.0.0" {
		t.Fatalf("plan = %#v", report.Plan)
	}
	if official.calls != 0 || official.lookupCalls != 2 || official.exactCalls != 2 || github.input != "" {
		t.Fatalf("exact resolution calls: Resolve=%d Lookup=%d ResolveExact=%d GitHub=%q", official.calls, official.lookupCalls, official.exactCalls, github.input)
	}
}

func TestInstallDryRunReportsAssetPreflightFailureBeforePrintingPlan(t *testing.T) {
	for _, test := range []struct {
		name    string
		assets  map[string]string
		problem string
	}{
		{name: "missing main.js", assets: map[string]string{"/owner/demo/releases/download/1.0.0/manifest.json": `{"id":"demo","version":"1.0.0","minAppVersion":"1.0.0"}`}, problem: "HTTP 404"},
		{name: "mismatched manifest", assets: map[string]string{"/owner/demo/releases/download/1.0.0/manifest.json": `{"id":"demo","version":"2.0.0","minAppVersion":"1.0.0"}`, "/owner/demo/releases/download/1.0.0/main.js": "plugin"}, problem: "manifest version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			vaultRoot := newVault(t)
			release := officialRelease("demo", "1.0.0")
			release.Assets = source.ReleaseAssets{
				Manifest: source.AssetRef{Name: "manifest.json", URL: "https://github.com/owner/demo/releases/download/1.0.0/manifest.json"},
				MainJS:   source.AssetRef{Name: "main.js", URL: "https://github.com/owner/demo/releases/download/1.0.0/main.js"},
			}
			stager := stage.New(stage.Config{Client: assetDoer{assets: test.assets}})
			changer := &fakeChanger{}
			planPrinted := false
			report, err := manager.NewWithConfig(vaultRoot, manager.Config{
				Official: &fakeOfficialResolver{release: release}, Stager: stager, Change: changer,
				LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
				PlanReady:  func(model.Report) error { planPrinted = true; return nil },
			}).Run(context.Background(), model.Operation{Kind: model.OperationInstall, Install: model.InstallOptions{
				Inputs: []string{"demo"}, ObsidianVersion: "1.8.0", DryRun: true,
			}})
			if err == nil || !strings.Contains(err.Error(), test.problem) || planPrinted || report.Category != model.ResultPreflightFailure || changer.beginCalls != 0 {
				t.Fatalf("err = %v, plan printed = %t, report = %#v, begin calls = %d", err, planPrinted, report, changer.beginCalls)
			}
			if _, statErr := os.Stat(filepath.Join(vaultRoot, ".obsidian", ".plugman")); !os.IsNotExist(statErr) {
				t.Fatalf("dry-run created Vault-local Plugman state: %v", statErr)
			}
		})
	}
}

func TestInstallDryRunRejectsUnsafeCurrentStateWithoutLockOrPlan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires additional privileges on Windows")
	}
	vaultRoot := newVault(t)
	target := filepath.Join(vaultRoot, ".obsidian", "plugins", "demo")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), target); err != nil {
		t.Fatal(err)
	}
	stager := &fakeStager{t: t}
	planPrinted := false
	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: &fakeOfficialResolver{release: officialRelease("demo", "1.0.0")}, Stager: stager,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
		PlanReady:  func(model.Report) error { planPrinted = true; return nil },
	}).Run(context.Background(), model.Operation{Kind: model.OperationInstall, Install: model.InstallOptions{
		Inputs: []string{"demo"}, ObsidianVersion: "1.8.0", DryRun: true,
	}})
	if err == nil || planPrinted || report.Category != model.ResultPreflightFailure {
		t.Fatalf("err = %v, plan printed = %t, report = %#v", err, planPrinted, report)
	}
	if _, statErr := os.Stat(filepath.Join(vaultRoot, ".obsidian", ".plugman")); !os.IsNotExist(statErr) {
		t.Fatalf("dry-run created Vault-local Plugman state: %v", statErr)
	}
}

func TestInstallDryRunPerformsSafeRuntimePreflight(t *testing.T) {
	vaultRoot := newVault(t)
	stager := &fakeStager{t: t}
	changer := &fakeChanger{}
	planPrinted := false
	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: &fakeOfficialResolver{release: officialRelease("demo", "1.0.0")}, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrCLIUnsupported},
		PlanReady:  func(model.Report) error { planPrinted = true; return nil },
	}).Run(context.Background(), model.Operation{Kind: model.OperationInstall, Install: model.InstallOptions{
		Inputs: []string{"demo"}, ObsidianVersion: "1.8.0", DryRun: true,
	}})
	if !errors.Is(err, obsidian.ErrCLIUnsupported) || planPrinted || report.Category != model.ResultPreflightFailure || changer.beginCalls != 0 {
		t.Fatalf("err = %v, plan printed = %t, report = %#v, begin calls = %d", err, planPrinted, report, changer.beginCalls)
	}
}

func TestInstallClassifiesInputErrorAsPreflightFailure(t *testing.T) {
	vaultRoot := newVault(t)
	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{
		Kind: model.OperationInstall, Install: model.InstallOptions{Inputs: []string{"missing.plugins"}, ObsidianVersion: "1.8.0"},
	})
	if err == nil || report.SchemaVersion != 1 || report.Category != model.ResultPreflightFailure {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
}

func TestInstallPreflightsCompleteBatchBeforeApplyingInDeclarationOrder(t *testing.T) {
	vaultRoot := newVault(t)
	stager := &fakeStager{t: t}
	planPrinted := false
	changer := &fakeChanger{stager: stager, planReady: &planPrinted}
	resolver := &mapOfficialResolver{releases: map[string]source.Release{
		"first":  officialRelease("first", "1.0.0"),
		"second": officialRelease("second", "2.0.0"),
	}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: resolver, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
		PlanReady:  func(model.Report) error { planPrinted = true; return nil },
	}).Run(context.Background(), model.Operation{
		Kind:    model.OperationInstall,
		Install: model.InstallOptions{Inputs: []string{"first", "second"}, ObsidianVersion: "1.8.0", Enable: true},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := stager.releaseIDs; !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("staged releases = %v", got)
	}
	if got := changer.pluginIDs(); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("applied plugins = %v", got)
	}
	for _, request := range changer.requests {
		if request.Kind != change.Install || request.Enabled != change.Enable {
			t.Errorf("change = %#v, want enabled install", request)
		}
	}
	if report.Category != model.ResultSuccess || len(report.Results) != 2 || !report.Results[0].Changed || !report.Results[1].Changed {
		t.Fatalf("report = %#v", report)
	}
}

func TestInstallPreflightFailureAppliesNothing(t *testing.T) {
	vaultRoot := newVault(t)
	stager := &fakeStager{t: t, err: errors.New("asset validation failed")}
	changer := &fakeChanger{stager: stager}
	resolver := &mapOfficialResolver{releases: map[string]source.Release{
		"first":  officialRelease("first", "1.0.0"),
		"second": officialRelease("second", "1.0.0"),
	}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: resolver, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{
		Kind: model.OperationInstall, Install: model.InstallOptions{Inputs: []string{"first", "second"}, ObsidianVersion: "1.8.0"},
	})
	if err == nil || len(changer.requests) != 0 || report.Category != model.ResultPreflightFailure {
		t.Fatalf("err = %v, changes = %v, report = %#v", err, changer.requests, report)
	}
}

func TestInstallValidatesEveryPreparedChangeBeforeFirstApply(t *testing.T) {
	vaultRoot := newVault(t)
	stager := &fakeStager{t: t}
	changer := &fakeChanger{stager: stager, validateErrAt: 1}
	resolver := &mapOfficialResolver{releases: map[string]source.Release{
		"first":  officialRelease("first", "1.0.0"),
		"second": officialRelease("second", "1.0.0"),
	}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: resolver, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{
		Kind: model.OperationInstall, Install: model.InstallOptions{Inputs: []string{"first", "second"}, ObsidianVersion: "1.8.0"},
	})
	if err == nil || changer.validationCalls != 2 || len(changer.requests) != 0 || report.Category != model.ResultPreflightFailure {
		t.Fatalf("err = %v, validation calls = %d, changes = %v, report = %#v", err, changer.validationCalls, changer.requests, report)
	}
}

func TestInstallResultsRetainDeclarationOrderWhenSomePluginsAreUnchanged(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "first", "manifest.json"), `{"id":"first","version":"1.0.0"}`)
	stager := &fakeStager{t: t}
	resolver := &mapOfficialResolver{releases: map[string]source.Release{
		"first":  officialRelease("first", "2.0.0"),
		"second": officialRelease("second", "1.0.0"),
	}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: resolver, Stager: stager, Change: &fakeChanger{stager: stager},
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{Kind: model.OperationInstall, Install: model.InstallOptions{
		Inputs: []string{"first", "second"}, ObsidianVersion: "1.8.0",
	}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Results) != 2 || report.Results[0].ID != "first" || report.Results[0].Action != model.PlanUnchanged || report.Results[1].ID != "second" || !report.Results[1].Changed {
		t.Fatalf("results = %#v", report.Results)
	}
}

func TestInstallPassesGitHubSourceRecordToChangeEngine(t *testing.T) {
	vaultRoot := newVault(t)
	stager := &fakeStager{t: t}
	changer := &fakeChanger{stager: stager}
	repository := "https://github.com/owner/demo"
	github := &fakeGitHubResolver{
		release:    officialRelease("demo", "1.2.3"),
		provenance: source.GitHubProvenance{Repository: repository, Release: "1.2.3"},
	}

	_, err := manager.NewWithConfig(vaultRoot, manager.Config{
		GitHub: github, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{
		Kind:    model.OperationInstall,
		Install: model.InstallOptions{Inputs: []string{repository}, ObsidianVersion: "1.8.0"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if changer.sourceRecord.Repository != repository || changer.sourceRecord.Release != "1.2.3" {
		t.Fatalf("Source Record = %#v", changer.sourceRecord)
	}
}

func TestInstallStopsAfterFailureAndReportsRestoredPartialResult(t *testing.T) {
	vaultRoot := newVault(t)
	stager := &fakeStager{t: t}
	changer := &fakeChanger{stager: stager, failAt: 1}
	resolver := &mapOfficialResolver{releases: map[string]source.Release{
		"first":  officialRelease("first", "1.0.0"),
		"second": officialRelease("second", "1.0.0"),
		"third":  officialRelease("third", "1.0.0"),
	}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: resolver, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{
		Kind:    model.OperationInstall,
		Install: model.InstallOptions{Inputs: []string{"first", "second", "third"}, ObsidianVersion: "1.8.0"},
	})
	if err == nil {
		t.Fatal("Run() error = nil")
	}
	if got := changer.pluginIDs(); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("applied plugins = %v, want stop after second", got)
	}
	if report.Category != model.ResultPartialFailure || len(report.Results) != 2 || !report.Results[0].Changed || !report.Results[1].Restored || report.Results[1].Error == "" {
		t.Fatalf("report = %#v", report)
	}
}

func TestInstallSingleRestoredFailureIsPreflightFailure(t *testing.T) {
	vaultRoot := newVault(t)
	stager := &fakeStager{t: t}
	applyErr := errors.New("simulated restored install failure")
	changer := &fakeChanger{stager: stager, liveOutcome: change.Outcome{PluginID: "demo", Restored: true}, liveErr: applyErr}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: &fakeOfficialResolver{release: officialRelease("demo", "1.0.0")}, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{states: map[string]obsidian.PluginState{}},
	}).Run(context.Background(), model.Operation{Kind: model.OperationInstall, Install: model.InstallOptions{
		Inputs: []string{"demo"}, ObsidianVersion: "1.8.0",
	}})
	if !errors.Is(err, applyErr) || report.Category != model.ResultPreflightFailure || len(report.Results) != 1 || !report.Results[0].Restored || report.Results[0].Changed {
		t.Fatalf("err = %v, report = %#v", err, report)
	}
}

func TestInstallUsesClosedChangeEngineOnlyWhenProbeConfirmsStopped(t *testing.T) {
	vaultRoot := newVault(t)
	stager := &fakeStager{t: t}
	changer := &fakeChanger{stager: stager}
	live := &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning}

	_, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: &fakeOfficialResolver{release: officialRelease("demo", "1.0.0")},
		Stager:   stager, Change: changer, LiveClient: live,
	}).Run(context.Background(), model.Operation{Kind: model.OperationInstall, Install: model.InstallOptions{
		Inputs: []string{"demo"}, ObsidianVersion: "1.8.0",
	}})
	if err != nil || changer.liveCalls != 0 || len(changer.requests) != 1 {
		t.Fatalf("err = %v, live calls = %d, changes = %#v", err, changer.liveCalls, changer.requests)
	}
}

func TestInstallRefusesMutationWhenProductionCLISelectsAnotherVault(t *testing.T) {
	root := t.TempDir()
	vaultRoot := filepath.Join(root, "target", "Notes")
	otherVault := filepath.Join(root, "other", "Notes")
	for _, path := range []string{vaultRoot, otherVault} {
		if err := os.MkdirAll(filepath.Join(path, ".obsidian", "plugins"), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(path, ".obsidian", "community-plugins.json"), `[]`)
	}
	runner := &vaultSelectionRunner{selected: otherVault}
	live := obsidian.NewCLIClient(obsidian.CLIConfig{
		VaultPath: vaultRoot, Runner: runner, Detector: runningRuntimeDetector{},
	})
	stager := &fakeStager{t: t}
	changer := &fakeChanger{stager: stager}

	_, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: &fakeOfficialResolver{release: officialRelease("demo", "1.0.0")},
		Stager:   stager, Change: changer, LiveClient: live,
	}).Run(context.Background(), model.Operation{Kind: model.OperationInstall, Install: model.InstallOptions{
		Inputs: []string{"demo"}, ObsidianVersion: "1.8.0",
	}})
	if !errors.Is(err, obsidian.ErrCLIUnsupported) || changer.liveCalls != 0 || len(changer.requests) != 0 {
		t.Fatalf("err = %v, live calls = %d, changes = %#v", err, changer.liveCalls, changer.requests)
	}
	if _, statErr := os.Stat(filepath.Join(vaultRoot, ".obsidian", "plugins", "demo")); !os.IsNotExist(statErr) {
		t.Fatalf("plugin filesystem was mutated: %v", statErr)
	}
	if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].Args, []string{"vault", "info=path"}) {
		t.Fatalf("CLI commands = %+v; lifecycle command must not run", runner.commands)
	}
}

func TestInstallUsesLiveSessionAndEnableNewPolicyWhenObsidianIsOpen(t *testing.T) {
	vaultRoot := newVault(t)
	stager := &fakeStager{t: t}
	changer := &fakeChanger{stager: stager}
	live := &fakeLiveClient{states: map[string]obsidian.PluginState{"demo": {}}}
	var plan obsidian.ChangePlan

	_, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: &fakeOfficialResolver{release: officialRelease("demo", "1.0.0")}, Stager: stager, Change: changer, LiveClient: live,
		LiveSession: func(value obsidian.ChangePlan) change.RuntimeSession { plan = value; return noopRuntimeSession{} },
	}).Run(context.Background(), model.Operation{Kind: model.OperationInstall, Install: model.InstallOptions{
		Inputs: []string{"demo"}, ObsidianVersion: "1.8.0", Enable: true,
	}})
	if err != nil || changer.liveCalls != 1 || plan.PluginID != "demo" || plan.TargetVersion != "1.0.0" || !plan.EnableNew || plan.TargetAbsent {
		t.Fatalf("err = %v, live calls = %d, plan = %#v", err, changer.liveCalls, plan)
	}
}

func TestLiveUpdatePreservesPlannedRuntimeStateAndRefusesUnsupportedRuntime(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "manifest.json"), `{"id":"demo","version":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "community-plugins.json"), `["demo"]`)
	stager := &fakeStager{t: t}
	state := obsidian.PluginState{Present: true, ID: "demo", Version: "1.0.0", Enabled: true, Loaded: true}
	changer := &fakeChanger{stager: stager}
	var plan obsidian.ChangePlan
	configured := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: &fakeOfficialResolver{release: officialRelease("demo", "2.0.0")}, Stager: stager, Change: changer,
		LiveClient:  &fakeLiveClient{states: map[string]obsidian.PluginState{"demo": state}},
		LiveSession: func(value obsidian.ChangePlan) change.RuntimeSession { plan = value; return noopRuntimeSession{} },
	})
	_, err := configured.Run(context.Background(), model.Operation{Kind: model.OperationUpdate, Update: model.UpdateOptions{Inputs: []string{"demo"}, ObsidianVersion: "1.8.0"}})
	if err != nil || plan.PlannedState != state || plan.EnableNew || changer.liveCalls != 1 {
		t.Fatalf("err = %v, plan = %#v, live calls = %d", err, plan, changer.liveCalls)
	}

	refusingChanger := &fakeChanger{stager: stager}
	_, err = manager.NewWithConfig(vaultRoot, manager.Config{
		Official: &fakeOfficialResolver{release: officialRelease("demo", "2.0.0")}, Stager: stager, Change: refusingChanger,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrCLIUnsupported},
	}).Run(context.Background(), model.Operation{Kind: model.OperationUpdate, Update: model.UpdateOptions{Inputs: []string{"demo"}, ObsidianVersion: "1.8.0"}})
	if !errors.Is(err, obsidian.ErrCLIUnsupported) || len(refusingChanger.requests) != 0 {
		t.Fatalf("unsupported err = %v, changes = %#v", err, refusingChanger.requests)
	}
}

func TestLiveSessionRecheckRejectsRuntimeStateChangeBeforeMutation(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "manifest.json"), `{"id":"demo","version":"1.0.0"}`)
	stager := &fakeStager{t: t}
	prior := obsidian.PluginState{Present: true, ID: "demo", Version: "1.0.0"}
	changed := prior
	changed.Enabled = true
	changed.Loaded = true
	changer := &fakeChanger{stager: stager, invokeSession: true}
	live := &fakeLiveClient{sequence: []obsidian.PluginState{prior, prior, changed}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: &fakeOfficialResolver{release: officialRelease("demo", "2.0.0")}, Stager: stager, Change: changer, LiveClient: live,
	}).Run(context.Background(), model.Operation{Kind: model.OperationUpdate, Update: model.UpdateOptions{Inputs: []string{"demo"}, ObsidianVersion: "1.8.0"}})
	if !errors.Is(err, obsidian.ErrStateChanged) || report.Category != model.ResultPreflightFailure || len(report.Results) != 1 || report.Results[0].Changed {
		t.Fatalf("err = %v, report = %#v, requests = %#v", err, report, changer.requests)
	}
}

func TestInterruptedRecoveryRunsBeforeInspectionAndInformsPlanning(t *testing.T) {
	vaultRoot := newVault(t)
	manifestPath := filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "manifest.json")
	writeFile(t, manifestPath, `{"id":"demo","version":"2.0.0"}`)
	stager := &fakeStager{t: t}
	changer := &fakeChanger{stager: stager, recoverFunc: func() error {
		return os.WriteFile(manifestPath, []byte(`{"id":"demo","version":"1.0.0"}`), 0o644)
	}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: &fakeOfficialResolver{release: officialRelease("demo", "2.0.0")}, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{Kind: model.OperationUpdate, Update: model.UpdateOptions{Inputs: []string{"demo"}, ObsidianVersion: "1.8.0"}})
	if err != nil || changer.beginCalls != 1 || changer.recoverCalls != 1 || changer.closeCalls != 1 || len(report.Plan) != 1 || report.Plan[0].CurrentVersion == nil || *report.Plan[0].CurrentVersion != "1.0.0" || report.Plan[0].Action != model.PlanUpgrade {
		t.Fatalf("err = %v, begin/recover/close = %d/%d/%d, plan = %#v", err, changer.beginCalls, changer.recoverCalls, changer.closeCalls, report.Plan)
	}
}

func TestRecoveryRequiredBlocksBeforeSourceResolution(t *testing.T) {
	vaultRoot := newVault(t)
	official := &fakeOfficialResolver{release: officialRelease("demo", "1.0.0")}
	changer := &fakeChanger{recoverFunc: func() error {
		return &change.RecoveryRequiredError{Path: filepath.Join(vaultRoot, ".obsidian", ".plugman", "recovery", "current"), Err: errors.New("live runtime verification required")}
	}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: official, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{
		Kind: model.OperationInstall, Install: model.InstallOptions{Inputs: []string{"demo"}, ObsidianVersion: "1.8.0"},
	})
	var recoveryRequired *change.RecoveryRequiredError
	if !errors.As(err, &recoveryRequired) || report.Category != model.ResultRecoveryRequired || official.calls != 0 {
		t.Fatalf("err = %v, report = %#v, resolver calls = %d", err, report, official.calls)
	}
}

func TestManagerHoldsOneVaultBatchLockAcrossPreflightAndApply(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "manifest.json"), `{"id":"demo","version":"1.0.0"}`)
	firstReady := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	operation := model.Operation{Kind: model.OperationUninstall, Uninstall: model.UninstallOptions{IDs: []string{"demo"}, Yes: true}}
	first := manager.NewWithConfig(vaultRoot, manager.Config{
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
		PlanReady: func(model.Report) error {
			close(firstReady)
			<-releaseFirst
			return nil
		},
	})
	go func() {
		_, err := first.Run(context.Background(), operation)
		firstDone <- err
	}()
	select {
	case <-firstReady:
	case <-time.After(2 * time.Second):
		t.Fatal("first Manager did not reach locked preflight")
	}

	second := manager.NewWithConfig(vaultRoot, manager.Config{LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning}})
	_, secondErr := second.Run(context.Background(), operation)
	if secondErr == nil || !strings.Contains(secondErr.Error(), "another Plugman mutation") {
		t.Fatalf("competing Manager error = %v", secondErr)
	}
	close(releaseFirst)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first Manager error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first Manager did not finish after releasing preflight")
	}
}

func TestLiveReloadFailureReportsRestoredPreflightOrRecoveryRequired(t *testing.T) {
	for _, test := range []struct {
		name     string
		outcome  change.Outcome
		err      error
		category model.ResultCategory
	}{
		{name: "restored", outcome: change.Outcome{PluginID: "demo", Restored: true}, err: errors.New("reload plugin: failed"), category: model.ResultPreflightFailure},
		{name: "recovery required", err: &change.RecoveryRequiredError{Path: "/recovery", Err: errors.New("runtime rollback failed")}, category: model.ResultRecoveryRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			vaultRoot := newVault(t)
			writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "manifest.json"), `{"id":"demo","version":"1.0.0"}`)
			stager := &fakeStager{t: t}
			changer := &fakeChanger{stager: stager, liveOutcome: test.outcome, liveErr: test.err}
			state := obsidian.PluginState{Present: true, ID: "demo", Version: "1.0.0"}
			report, err := manager.NewWithConfig(vaultRoot, manager.Config{
				Official: &fakeOfficialResolver{release: officialRelease("demo", "2.0.0")}, Stager: stager, Change: changer,
				LiveClient: &fakeLiveClient{states: map[string]obsidian.PluginState{"demo": state}},
			}).Run(context.Background(), model.Operation{Kind: model.OperationUpdate, Update: model.UpdateOptions{Inputs: []string{"demo"}, ObsidianVersion: "1.8.0"}})
			if !errors.Is(err, test.err) || report.Category != test.category {
				t.Fatalf("err = %v, report = %#v", err, report)
			}
		})
	}
}

func TestTargetedUpdateAdvancesUnversionedPluginAndPreservesState(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "manifest.json"), `{"id":"demo","version":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "data.json"), `{"setting":true}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "community-plugins.json"), `["demo"]`)
	stager := &fakeStager{t: t}
	changer := &fakeChanger{stager: stager}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: &fakeOfficialResolver{release: officialRelease("demo", "2.0.0")}, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{Kind: model.OperationUpdate, Update: model.UpdateOptions{
		Inputs: []string{"demo"}, ObsidianVersion: "1.8.0",
	}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Plan) != 1 || report.Plan[0].Action != model.PlanUpgrade {
		t.Fatalf("plan = %#v", report.Plan)
	}
	if len(changer.requests) != 1 || changer.requests[0].Kind != change.Update || changer.requests[0].Enabled != change.PreserveEnabled {
		t.Fatalf("change = %#v", changer.requests)
	}
}

func TestTargetedUpdateAppliesExactDeclaredVersion(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "manifest.json"), `{"id":"demo","version":"1.0.0"}`)
	stager := &fakeStager{t: t}
	github := &fakeGitHubResolver{release: officialRelease("demo", "2.0.0")}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: &fakeOfficialResolver{release: officialRelease("demo", "3.0.0")}, GitHub: github, Stager: stager, Change: &fakeChanger{stager: stager},
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{Kind: model.OperationUpdate, Update: model.UpdateOptions{
		Inputs: []string{"demo@2.0.0"}, ObsidianVersion: "1.8.0",
	}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Plan) != 1 || report.Plan[0].Action != model.PlanUpgrade || report.Plan[0].TargetVersion != "2.0.0" || stager.calls != 1 || !reflect.DeepEqual(stager.releaseIDs, []string{"demo"}) {
		t.Fatalf("report = %#v, staged = %v", report, stager.releaseIDs)
	}
}

func TestBareUpdateIncludesAllInstalledPluginsWithResolvableSources(t *testing.T) {
	vaultRoot := newVault(t)
	officialRoot := filepath.Join(vaultRoot, ".obsidian", "plugins", "official")
	writeFile(t, filepath.Join(officialRoot, "manifest.json"), `{"id":"official","version":"1.0.0"}`)
	writeFile(t, filepath.Join(officialRoot, ".plugman.json"), `{"repository":"https://github.com/fork/official","release":"1.0.0"}`)
	githubRoot := filepath.Join(vaultRoot, ".obsidian", "plugins", "github-only")
	writeFile(t, filepath.Join(githubRoot, "manifest.json"), `{"id":"github-only","version":"1.0.0"}`)
	writeFile(t, filepath.Join(githubRoot, ".plugman.json"), `{"repository":"https://github.com/owner/github-only","release":"1.0.0"}`)
	officialGitHubRoot := filepath.Join(vaultRoot, ".obsidian", "plugins", "official-github")
	writeFile(t, filepath.Join(officialGitHubRoot, "manifest.json"), `{"id":"official-github","version":"1.0.0"}`)
	writeFile(t, filepath.Join(officialGitHubRoot, ".plugman.json"), `{"repository":"https://github.com/owner/official-github","release":"1.0.0"}`)
	stager := &fakeStager{t: t}
	changer := &fakeChanger{stager: stager}
	official := &recognizingOfficialResolver{releases: map[string]source.Release{
		"official": officialRelease("official", "2.0.0"), "official-github": officialRelease("official-github", "2.0.0"),
	}}
	github := &fakeGitHubResolver{
		release:    officialRelease("github-only", "2.0.0"),
		provenance: source.GitHubProvenance{Repository: "https://github.com/owner/github-only", Release: "2.0.0"},
	}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: official, GitHub: github, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{
		Kind: model.OperationUpdate, Update: model.UpdateOptions{ObsidianVersion: "1.8.0"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	actions := map[string]model.PlanAction{}
	for _, item := range report.Plan {
		actions[item.ID] = item.Action
	}
	if len(report.Plan) != 3 || actions["official"] != model.PlanUpgrade || actions["official-github"] != model.PlanUpgrade || actions["github-only"] != model.PlanUpgrade {
		t.Fatalf("plan = %#v", report.Plan)
	}
	if got := changer.pluginIDs(); !reflect.DeepEqual(got, []string{"github-only", "official", "official-github"}) {
		t.Fatalf("updated = %v", got)
	}
	if github.input != "https://github.com/owner/github-only" {
		t.Fatalf("GitHub resolved input = %q", github.input)
	}
}

func TestUpdateEnabledOnlyFiltersBareUpdateToEnabledPlugins(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "enabled", "manifest.json"), `{"id":"enabled","version":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "disabled", "manifest.json"), `{"id":"disabled","version":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "community-plugins.json"), `["enabled"]`)
	stager := &fakeStager{t: t}
	changer := &fakeChanger{stager: stager}
	official := &recognizingOfficialResolver{releases: map[string]source.Release{
		"enabled":  officialRelease("enabled", "2.0.0"),
		"disabled": officialRelease("disabled", "2.0.0"),
	}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: official, Stager: stager, Change: changer,
		LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{
		Kind: model.OperationUpdate, Update: model.UpdateOptions{ObsidianVersion: "1.8.0", EnabledOnly: true},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Plan) != 1 || report.Plan[0].ID != "enabled" {
		t.Fatalf("plan = %#v, want only enabled plugin", report.Plan)
	}
	if got := changer.pluginIDs(); !reflect.DeepEqual(got, []string{"enabled"}) {
		t.Fatalf("updated = %v", got)
	}
}

func TestOutdatedReportsOfficialGitHubAndUnknownStates(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "official", "manifest.json"), `{"id":"official","version":"1.0.0"}`)
	githubRoot := filepath.Join(vaultRoot, ".obsidian", "plugins", "github-only")
	writeFile(t, filepath.Join(githubRoot, "manifest.json"), `{"id":"github-only","version":"1.0.0"}`)
	writeFile(t, filepath.Join(githubRoot, ".plugman.json"), `{"repository":"https://github.com/owner/github-only","release":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "local", "manifest.json"), `{"id":"local","version":"1.0.0"}`)
	official := &recognizingOfficialResolver{releases: map[string]source.Release{"official": officialRelease("official", "2.0.0")}}
	github := &fakeGitHubResolver{release: officialRelease("github-only", "1.0.0"), provenance: source.GitHubProvenance{Repository: "https://github.com/owner/github-only", Release: "1.0.0"}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{Official: official, GitHub: github}).Run(context.Background(), model.Operation{
		Kind: model.OperationOutdated, Outdated: model.OutdatedOptions{ObsidianVersion: "1.8.0"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	states := map[string]model.OutdatedState{}
	for _, item := range report.Outdated {
		states[item.ID] = item.State
	}
	if states["official"] != model.OutdatedAvailable || states["github-only"] != model.OutdatedCurrent || states["local"] != model.OutdatedUnknownSource {
		t.Fatalf("outdated = %#v", report.Outdated)
	}
}

func TestOutdatedKeepsGitHubResultsAndClassifiesLocalsWhenRegistryIsUnavailable(t *testing.T) {
	vaultRoot := newVault(t)
	githubRoot := filepath.Join(vaultRoot, ".obsidian", "plugins", "github-plugin")
	writeFile(t, filepath.Join(githubRoot, "manifest.json"), `{"id":"github-plugin","version":"1.0.0"}`)
	writeFile(t, filepath.Join(githubRoot, ".plugman.json"), `{"repository":"https://github.com/owner/github-plugin","release":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "local-a", "manifest.json"), `{"id":"local-a","version":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "local-b", "manifest.json"), `{"id":"local-b","version":"1.0.0"}`)
	recognizer := &failingRecognizer{err: errors.New("official registry unavailable")}
	official := &fakeOfficialResolver{}
	github := &fakeGitHubResolver{
		release:    officialRelease("github-plugin", "2.0.0"),
		provenance: source.GitHubProvenance{Repository: "https://github.com/owner/github-plugin", Release: "2.0.0"},
	}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Official: official, OfficialRecognition: recognizer, GitHub: github,
	}).Run(context.Background(), model.Operation{Kind: model.OperationOutdated, Outdated: model.OutdatedOptions{ObsidianVersion: "1.8.0"}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if recognizer.calls != 1 || official.calls != 0 {
		t.Fatalf("recognizer calls = %d, official release calls = %d", recognizer.calls, official.calls)
	}
	if len(report.Outdated) != 3 || report.Outdated[0].ID != "github-plugin" || report.Outdated[0].State != model.OutdatedAvailable || report.Outdated[0].Source.Kind != model.SourceGitHub || report.Outdated[1].ID != "local-a" || report.Outdated[1].State != model.OutdatedTransport || report.Outdated[2].ID != "local-b" || report.Outdated[2].State != model.OutdatedTransport {
		t.Fatalf("outdated = %#v", report.Outdated)
	}
	if report.Outdated[1].Problem == "" || report.Outdated[2].Problem == "" {
		t.Fatalf("transport problems missing: %#v", report.Outdated)
	}
}

func TestOutdatedEnabledOnlyFiltersOnEnabledState(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "enabled", "manifest.json"), `{"id":"enabled","version":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "disabled", "manifest.json"), `{"id":"disabled","version":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "community-plugins.json"), `["enabled"]`)
	official := &recognizingOfficialResolver{releases: map[string]source.Release{
		"enabled":  officialRelease("enabled", "2.0.0"),
		"disabled": officialRelease("disabled", "2.0.0"),
	}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{Official: official}).Run(context.Background(), model.Operation{
		Kind: model.OperationOutdated, Outdated: model.OutdatedOptions{ObsidianVersion: "1.8.0", EnabledOnly: true},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Outdated) != 1 || report.Outdated[0].ID != "enabled" {
		t.Fatalf("outdated = %#v, want only enabled plugin", report.Outdated)
	}
}

func TestUninstallRequiresConfirmationThenAppliesOrderedChanges(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "manifest.json"), `{"id":"demo","version":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "data.json"), `{}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "community-plugins.json"), `["demo"]`)
	stager := &fakeStager{t: t}
	changer := &fakeChanger{stager: stager}
	state := obsidian.PluginState{Present: true, ID: "demo", Version: "1.0.0", Enabled: true, Loaded: true}
	var livePlan obsidian.ChangePlan
	configured := manager.NewWithConfig(vaultRoot, manager.Config{
		Change: changer, LiveClient: &fakeLiveClient{states: map[string]obsidian.PluginState{"demo": state}},
		LiveSession: func(plan obsidian.ChangePlan) change.RuntimeSession { livePlan = plan; return noopRuntimeSession{} },
	})
	operation := model.Operation{Kind: model.OperationUninstall, Uninstall: model.UninstallOptions{IDs: []string{"demo"}, KeepData: true}}

	report, err := configured.Run(context.Background(), operation)
	var confirmation *model.ConfirmationRequiredError
	if !errors.As(err, &confirmation) || len(report.Uninstall) != 1 || !report.Uninstall[0].HasData || !report.Uninstall[0].Enabled {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	operation.Uninstall.Yes = true
	report, err = configured.Run(context.Background(), operation)
	if err != nil {
		t.Fatalf("confirmed Run() error = %v", err)
	}
	if len(changer.requests) != 1 || changer.requests[0].Kind != change.Uninstall || !changer.requests[0].KeepData || report.Results[0].Action != model.PlanUninstall || changer.liveCalls != 1 || !livePlan.TargetAbsent || livePlan.PlannedState != state {
		t.Fatalf("requests = %#v, report = %#v", changer.requests, report)
	}
}

func TestUninstallSingleRestoredFailureIsPreflightFailure(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "demo", "manifest.json"), `{"id":"demo","version":"1.0.0"}`)
	applyErr := errors.New("simulated restored uninstall failure")
	failAt := 0
	changer := &fakeChanger{failIndex: &failAt, applyErr: applyErr, applyOutcome: change.Outcome{PluginID: "demo", Restored: true}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Change: changer, LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{Kind: model.OperationUninstall, Uninstall: model.UninstallOptions{IDs: []string{"demo"}, Yes: true}})
	if !errors.Is(err, applyErr) || report.Category != model.ResultPreflightFailure || len(report.Results) != 1 || !report.Results[0].Restored || report.Results[0].Changed {
		t.Fatalf("err = %v, report = %#v", err, report)
	}
}

func TestUninstallEarlierSuccessMakesLaterRestoredFailurePartial(t *testing.T) {
	vaultRoot := newVault(t)
	for _, id := range []string{"first", "second"} {
		writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", id, "manifest.json"), fmt.Sprintf(`{"id":%q,"version":"1.0.0"}`, id))
	}
	applyErr := errors.New("simulated restored uninstall failure")
	failAt := 1
	changer := &fakeChanger{failIndex: &failAt, applyErr: applyErr, applyOutcome: change.Outcome{PluginID: "second", Restored: true}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{
		Change: changer, LiveClient: &fakeLiveClient{probeErr: obsidian.ErrObsidianNotRunning},
	}).Run(context.Background(), model.Operation{Kind: model.OperationUninstall, Uninstall: model.UninstallOptions{IDs: []string{"first", "second"}, Yes: true}})
	if !errors.Is(err, applyErr) || report.Category != model.ResultPartialFailure || len(report.Results) != 2 || !report.Results[0].Changed || !report.Results[1].Restored {
		t.Fatalf("err = %v, report = %#v", err, report)
	}
}

func TestExportRecognizesOfficialPluginsAndRoundTrips(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "official", "manifest.json"), `{"id":"official","version":"1.0.0"}`)
	githubRoot := filepath.Join(vaultRoot, ".obsidian", "plugins", "github-only")
	writeFile(t, filepath.Join(githubRoot, "manifest.json"), `{"id":"github-only","version":"1.2.3"}`)
	writeFile(t, filepath.Join(githubRoot, ".plugman.json"), `{"repository":"https://github.com/owner/github-only","release":"1.2.3"}`)
	official := &recognizingOfficialResolver{releases: map[string]source.Release{"official": officialRelease("official", "2.0.0")}}

	report, err := manager.NewWithConfig(vaultRoot, manager.Config{Official: official}).Run(context.Background(), model.Operation{
		Kind: model.OperationExport, Export: model.ExportOptions{Path: "saved.plugins", ObsidianVersion: "1.8.0"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(vaultRoot, "saved.plugins"))
	if err != nil {
		t.Fatal(err)
	}
	want := "https://github.com/owner/github-only/releases/tag/1.2.3\nofficial@1.0.0\n"
	if string(contents) != want || report.Export == nil || report.Export.Written != 2 {
		t.Fatalf("contents = %q, report = %#v", contents, report)
	}
	declarations, err := plugininput.Expand(vaultRoot, []string{"saved.plugins"})
	if err != nil || len(declarations) != 2 {
		t.Fatalf("round trip declarations = %#v, err = %v", declarations, err)
	}
}

func TestListReportsFolderWithMissingManifestAsInvalidPluginState(t *testing.T) {
	vaultRoot := newVault(t)
	if err := os.MkdirAll(filepath.Join(vaultRoot, ".obsidian", "plugins", "broken"), 0o755); err != nil {
		t.Fatalf("create plugin folder: %v", err)
	}

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []model.PluginObservation{{
		Folder:   "broken",
		Source:   model.PluginSource{Kind: model.SourceUnknown},
		Status:   model.PluginInvalid,
		Problems: []model.Problem{{Code: "manifest_missing", Message: "manifest.json is missing"}},
	}}
	if !reflect.DeepEqual(report.Plugins, want) {
		t.Errorf("plugins = %#v, want %#v", report.Plugins, want)
	}
}

func TestListReportsInvalidManifestJSON(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "broken", "manifest.json"), `{`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(report.Plugins) != 1 {
		t.Fatalf("len(plugins) = %d, want 1", len(report.Plugins))
	}
	plugin := report.Plugins[0]
	if plugin.Status != model.PluginInvalid {
		t.Errorf("status = %q, want %q", plugin.Status, model.PluginInvalid)
	}
	if len(plugin.Problems) != 1 || plugin.Problems[0].Code != "manifest_invalid_json" {
		t.Errorf("problems = %#v, want manifest_invalid_json", plugin.Problems)
	}
}

func TestListReportsMissingManifestIdentity(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "broken", "manifest.json"), `{"version":"1.0.0"}`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	plugin := report.Plugins[0]
	if plugin.Status != model.PluginInvalid || plugin.ID != nil || plugin.Version == nil {
		t.Errorf("plugin = %#v, want invalid state with unknown ID and known version", plugin)
	}
	if len(plugin.Problems) != 1 || plugin.Problems[0].Code != "manifest_id_invalid" {
		t.Errorf("problems = %#v, want manifest_id_invalid", plugin.Problems)
	}
}

func TestListReportsMissingManifestVersion(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "broken", "manifest.json"), `{"id":"broken"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "community-plugins.json"), `["broken"]`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	plugin := report.Plugins[0]
	if plugin.Status != model.PluginInvalid || plugin.ID == nil || plugin.Version != nil || plugin.Enabled == nil || !*plugin.Enabled {
		t.Errorf("plugin = %#v, want invalid enabled state with known ID", plugin)
	}
	if len(plugin.Problems) != 1 || plugin.Problems[0].Code != "manifest_version_invalid" {
		t.Errorf("problems = %#v, want manifest_version_invalid", plugin.Problems)
	}
}

func TestListReportsWrongManifestFieldType(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "broken", "manifest.json"), `{"id":42,"version":"1.0.0"}`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	plugin := report.Plugins[0]
	if len(plugin.Problems) != 1 || plugin.Problems[0].Code != "manifest_id_invalid" {
		t.Errorf("problems = %#v, want manifest_id_invalid", plugin.Problems)
	}
}

func TestListReportsManifestIdentityFolderMismatch(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "wrong-folder", "manifest.json"), `{"id":"actual-id","version":"1.0.0"}`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	plugin := report.Plugins[0]
	if plugin.Status != model.PluginInvalid || plugin.ID == nil || *plugin.ID != "actual-id" {
		t.Errorf("plugin = %#v, want invalid state retaining actual ID", plugin)
	}
	if len(plugin.Problems) != 1 || plugin.Problems[0].Code != "manifest_id_mismatch" {
		t.Errorf("problems = %#v, want manifest_id_mismatch", plugin.Problems)
	}
}

func TestListReportsGitHubSourceRecord(t *testing.T) {
	vaultRoot := newVault(t)
	pluginRoot := filepath.Join(vaultRoot, ".obsidian", "plugins", "opencode")
	writeFile(t, filepath.Join(pluginRoot, "manifest.json"), `{"id":"opencode","version":"1.3.13"}`)
	writeFile(t, filepath.Join(pluginRoot, ".plugman.json"), `{"repository":"https://github.com/kriss-spy/obsidian-opencode","release":"1.3.13"}`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	wantRepository := "https://github.com/kriss-spy/obsidian-opencode"
	wantRelease := "1.3.13"
	want := model.PluginSource{Kind: model.SourceGitHub, Repository: &wantRepository, Release: &wantRelease}
	if !reflect.DeepEqual(report.Plugins[0].Source, want) {
		t.Errorf("source = %#v, want %#v", report.Plugins[0].Source, want)
	}
}

func TestListRejectsUntrustedSourceRecordRepository(t *testing.T) {
	vaultRoot := newVault(t)
	pluginRoot := filepath.Join(vaultRoot, ".obsidian", "plugins", "opencode")
	writeFile(t, filepath.Join(pluginRoot, "manifest.json"), `{"id":"opencode","version":"1.3.13"}`)
	writeFile(t, filepath.Join(pluginRoot, ".plugman.json"), `{"repository":"https://example.com/owner/repo","release":"1.3.13"}`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	plugin := report.Plugins[0]
	if plugin.Status != model.PluginInvalid || len(plugin.Problems) != 1 || plugin.Problems[0].Code != "source_record_invalid" {
		t.Errorf("plugin = %#v, want invalid source record", plugin)
	}
}

func TestListEnabledOnlyFiltersOnKnownEnabledState(t *testing.T) {
	vaultRoot := newVault(t)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "enabled", "manifest.json"), `{"id":"enabled","version":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "disabled", "manifest.json"), `{"id":"disabled","version":"1.0.0"}`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "plugins", "unknown", "manifest.json"), `{`)
	writeFile(t, filepath.Join(vaultRoot, ".obsidian", "community-plugins.json"), `["enabled"]`)

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{
		Kind: model.OperationList,
		List: model.ListOptions{EnabledOnly: true},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Plugins) != 1 || report.Plugins[0].ID == nil || *report.Plugins[0].ID != "enabled" {
		t.Errorf("plugins = %#v, want only enabled", report.Plugins)
	}
}

func TestListReportsSymlinkedPluginDirectoryAsInvalid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires additional privileges on Windows")
	}
	vaultRoot := newVault(t)
	target := t.TempDir()
	pluginPath := filepath.Join(vaultRoot, ".obsidian", "plugins", "linked")
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, pluginPath); err != nil {
		t.Fatal(err)
	}

	report, err := manager.New(vaultRoot).Run(context.Background(), model.Operation{Kind: model.OperationList})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Plugins) != 1 || report.Plugins[0].Problems[0].Code != "plugin_directory_symlink" {
		t.Errorf("plugins = %#v, want explicit symlink problem", report.Plugins)
	}
}

func newVault(t *testing.T) string {
	t.Helper()
	vaultRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vaultRoot, ".obsidian"), 0o755); err != nil {
		t.Fatalf("create Vault: %v", err)
	}
	return vaultRoot
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func pointer[T any](value T) *T {
	return &value
}

type fakeOfficialResolver struct {
	release      source.Release
	exactRelease source.Release
	err          error
	id           string
	target       source.Target
	calls        int
	lookupCalls  int
	exactCalls   int
}

type inspectingOfficialResolver struct {
	*fakeOfficialResolver
	inspectCalls int
}

type blockingTargetVersion struct {
	started chan<- string
	release <-chan struct{}
}

func (p *blockingTargetVersion) Latest(context.Context) (string, error) {
	p.started <- "target"
	<-p.release
	return "1.8.0", nil
}

type blockingRecognizer struct {
	started chan<- string
	release <-chan struct{}
}

func (r *blockingRecognizer) Recognize(context.Context, string) (bool, error) {
	r.started <- "directory"
	<-r.release
	return true, nil
}

func (r *inspectingOfficialResolver) Inspect(_ context.Context, id string, target source.Target) (source.Release, error) {
	r.inspectCalls++
	r.id, r.target = id, target
	return r.release, r.err
}

func (f *fakeOfficialResolver) Resolve(_ context.Context, id string, target source.Target) (source.Release, error) {
	f.calls++
	f.id, f.target = id, target
	return f.release, f.err
}

func (f *fakeOfficialResolver) Lookup(_ context.Context, id string) (source.OfficialPlugin, error) {
	f.lookupCalls++
	if f.err != nil {
		return source.OfficialPlugin{}, f.err
	}
	repository := f.release.Repository
	if f.exactRelease.Repository != "" {
		repository = f.exactRelease.Repository
	}
	return source.OfficialPlugin{ID: id, Repository: repository}, nil
}

func (f *fakeOfficialResolver) ResolveExact(_ context.Context, plugin source.OfficialPlugin, version string, target source.Target) (source.Release, error) {
	f.exactCalls++
	f.id, f.target = plugin.ID, target
	if f.err != nil {
		return source.Release{}, f.err
	}
	if f.exactRelease.PluginID != "" {
		return f.exactRelease, nil
	}
	release := f.release
	release.PluginID = plugin.ID
	release.Repository = plugin.Repository
	release.Version = version
	release.ReleaseURL = "https://github.com/" + plugin.Repository + "/releases/tag/" + version
	release.ReleaseManifest.Version = version
	return release, nil
}

type mapOfficialResolver struct {
	releases map[string]source.Release
}

type recognizingOfficialResolver struct {
	releases map[string]source.Release
}

type failingRecognizer struct {
	err   error
	calls int
}

func (r *failingRecognizer) Recognize(context.Context, string) (bool, error) {
	r.calls++
	return false, r.err
}

func (r *recognizingOfficialResolver) Recognize(_ context.Context, id string) (bool, error) {
	_, ok := r.releases[id]
	return ok, nil
}

func (r *recognizingOfficialResolver) Resolve(_ context.Context, id string, _ source.Target) (source.Release, error) {
	if release, ok := r.releases[id]; ok {
		return release, nil
	}
	return source.Release{}, &source.Error{Code: source.ErrorOfficialPluginNotFound, Operation: "resolve official plugin", Message: "not official"}
}

func (r *recognizingOfficialResolver) Lookup(_ context.Context, id string) (source.OfficialPlugin, error) {
	release, ok := r.releases[id]
	if !ok {
		return source.OfficialPlugin{}, fmt.Errorf("unknown fixture %s", id)
	}
	return source.OfficialPlugin{ID: id, Repository: release.Repository}, nil
}

func (r *recognizingOfficialResolver) ResolveExact(_ context.Context, plugin source.OfficialPlugin, version string, _ source.Target) (source.Release, error) {
	release, ok := r.releases[plugin.ID]
	if !ok {
		return source.Release{}, fmt.Errorf("unknown fixture %s", plugin.ID)
	}
	release.Version = version
	release.ReleaseManifest.Version = version
	return release, nil
}

func (f *mapOfficialResolver) Resolve(_ context.Context, id string, _ source.Target) (source.Release, error) {
	release, ok := f.releases[id]
	if !ok {
		return source.Release{}, fmt.Errorf("unknown fixture %s", id)
	}
	return release, nil
}

func (f *mapOfficialResolver) Lookup(_ context.Context, id string) (source.OfficialPlugin, error) {
	release, ok := f.releases[id]
	if !ok {
		return source.OfficialPlugin{}, fmt.Errorf("unknown fixture %s", id)
	}
	return source.OfficialPlugin{ID: id, Repository: release.Repository}, nil
}

func (f *mapOfficialResolver) ResolveExact(_ context.Context, plugin source.OfficialPlugin, version string, _ source.Target) (source.Release, error) {
	release, ok := f.releases[plugin.ID]
	if !ok {
		return source.Release{}, fmt.Errorf("unknown fixture %s", plugin.ID)
	}
	release.Version = version
	release.ReleaseManifest.Version = version
	return release, nil
}

func officialRelease(id, version string) source.Release {
	return source.Release{
		PluginID: id, Repository: "owner/" + id, Version: version,
		MinimumObsidianVersion: "1.0.0", ReleaseURL: "https://github.com/owner/" + id + "/releases/tag/" + version,
		ReleaseManifest: source.Manifest{ID: id, Name: id, Version: version},
	}
}

type fakeGitHubResolver struct {
	release    source.Release
	provenance source.GitHubProvenance
	err        error
	input      string
}

func (f *fakeGitHubResolver) Resolve(_ context.Context, input string, _ source.Target) (source.Release, source.GitHubProvenance, error) {
	f.input = input
	return f.release, f.provenance, f.err
}

type fakeStager struct {
	t          *testing.T
	calls      int
	releaseIDs []string
	prepared   []stage.Prepared
	err        error
	dryCalls   int
	dryErr     error
}

func (f *fakeStager) PreflightDryRun(_ context.Context, _ string, releases []source.Release) ([]stage.Prepared, error) {
	f.t.Helper()
	f.dryCalls++
	if f.dryErr != nil {
		return nil, f.dryErr
	}
	f.releaseIDs = nil
	f.prepared = nil
	batch := f.t.TempDir()
	for index, release := range releases {
		f.releaseIDs = append(f.releaseIDs, release.PluginID)
		directory := filepath.Join(batch, fmt.Sprintf("%03d-%s", index, release.PluginID))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			f.t.Fatal(err)
		}
		writeFile(f.t, filepath.Join(directory, "manifest.json"), fmt.Sprintf(`{"id":%q,"version":%q}`, release.PluginID, release.Version))
		writeFile(f.t, filepath.Join(directory, "main.js"), "plugin")
		f.prepared = append(f.prepared, stage.Prepared{Release: release, Dir: directory, BatchRoot: batch})
	}
	return f.prepared, nil
}

func (f *fakeStager) Preflight(_ context.Context, vaultRoot string, releases []source.Release) ([]stage.Prepared, error) {
	f.t.Helper()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	f.releaseIDs = nil
	f.prepared = nil
	batch := filepath.Join(vaultRoot, ".fake-staging")
	for index, release := range releases {
		f.releaseIDs = append(f.releaseIDs, release.PluginID)
		directory := filepath.Join(batch, fmt.Sprintf("%03d-%s", index, release.PluginID))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			f.t.Fatal(err)
		}
		writeFile(f.t, filepath.Join(directory, "manifest.json"), fmt.Sprintf(`{"id":%q,"version":%q}`, release.PluginID, release.Version))
		writeFile(f.t, filepath.Join(directory, "main.js"), "plugin")
		f.prepared = append(f.prepared, stage.Prepared{Release: release, Dir: directory, BatchRoot: batch})
	}
	return f.prepared, nil
}

type fakeChanger struct {
	stager          *fakeStager
	requests        []change.PreparedChange
	failAt          int
	validateErrAt   int
	validationCalls int
	sourceRecord    source.GitHubProvenance
	planReady       *bool
	liveCalls       int
	liveOutcome     change.Outcome
	liveErr         error
	invokeSession   bool
	recoverCalls    int
	recoverFunc     func() error
	beginCalls      int
	closeCalls      int
	batchErr        error
	failIndex       *int
	applyErr        error
	applyOutcome    change.Outcome
}

func (f *fakeChanger) BeginBatch(_ context.Context, _ string) (change.Batch, error) {
	f.beginCalls++
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	return &fakeBatch{changer: f}, nil
}

type fakeBatch struct{ changer *fakeChanger }

func (b *fakeBatch) Recover(ctx context.Context) (change.RecoveryOutcome, error) {
	return b.changer.Recover(ctx, "")
}
func (b *fakeBatch) Validate(ctx context.Context, request change.PreparedChange) error {
	return b.changer.Validate(ctx, request)
}
func (b *fakeBatch) Apply(ctx context.Context, request change.PreparedChange) (change.Outcome, error) {
	return b.changer.Apply(ctx, request)
}
func (b *fakeBatch) ApplyLive(ctx context.Context, request change.PreparedChange, runtime change.RuntimeSession) (change.Outcome, error) {
	return b.changer.ApplyLive(ctx, request, runtime)
}
func (b *fakeBatch) Close() error {
	b.changer.closeCalls++
	return nil
}

func (f *fakeChanger) Recover(_ context.Context, _ string) (change.RecoveryOutcome, error) {
	f.recoverCalls++
	if f.recoverFunc != nil {
		if err := f.recoverFunc(); err != nil {
			return change.RecoveryOutcome{}, err
		}
		return change.RecoveryOutcome{Recovered: true}, nil
	}
	return change.RecoveryOutcome{}, nil
}

func (f *fakeChanger) ApplyLive(ctx context.Context, request change.PreparedChange, runtime change.RuntimeSession) (change.Outcome, error) {
	f.liveCalls++
	if f.invokeSession {
		f.requests = append(f.requests, request)
		if err := runtime.Prepare(ctx); err != nil {
			return change.Outcome{PluginID: request.PluginID}, err
		}
		return change.Outcome{PluginID: request.PluginID, Changed: true}, nil
	}
	if f.liveErr != nil {
		f.requests = append(f.requests, request)
		return f.liveOutcome, f.liveErr
	}
	return f.Apply(ctx, request)
}

type fakeLiveClient struct {
	probeErr error
	states   map[string]obsidian.PluginState
	inspects []string
	sequence []obsidian.PluginState
}

type runningRuntimeDetector struct{}

func (runningRuntimeDetector) Detect(context.Context, string) (obsidian.RuntimeStatus, error) {
	return obsidian.RuntimeStatus{Running: true, CLISupported: true, SafeToInvoke: true}, nil
}

type vaultSelectionRunner struct {
	selected string
	commands []obsidian.Command
}

func (*vaultSelectionRunner) Available(string) bool { return true }

func (r *vaultSelectionRunner) Run(_ context.Context, command obsidian.Command) ([]byte, error) {
	r.commands = append(r.commands, command)
	if reflect.DeepEqual(command.Args, []string{"vault", "info=path"}) {
		return []byte(r.selected + "\n"), nil
	}
	return nil, errors.New("unexpected plugin lifecycle command")
}

func (f *fakeLiveClient) Probe(context.Context) error { return f.probeErr }
func (f *fakeLiveClient) Inspect(_ context.Context, id string) (obsidian.PluginState, error) {
	f.inspects = append(f.inspects, id)
	if len(f.sequence) != 0 {
		index := len(f.inspects) - 1
		if index >= len(f.sequence) {
			index = len(f.sequence) - 1
		}
		return f.sequence[index], nil
	}
	return f.states[id], nil
}
func (*fakeLiveClient) Disable(context.Context, string) error { return nil }
func (*fakeLiveClient) Unload(context.Context, string) error  { return nil }
func (*fakeLiveClient) Reload(context.Context, string) error  { return nil }
func (*fakeLiveClient) Enable(context.Context, string) error  { return nil }

type noopRuntimeSession struct{}

func (noopRuntimeSession) Prepare(context.Context) error  { return nil }
func (noopRuntimeSession) Commit(context.Context) error   { return nil }
func (noopRuntimeSession) Rollback(context.Context) error { return nil }

type assetDoer struct{ assets map[string]string }

func (d assetDoer) Do(request *http.Request) (*http.Response, error) {
	body, ok := d.assets[request.URL.Path]
	statusCode := http.StatusOK
	if !ok {
		body = "not found"
		statusCode = http.StatusNotFound
	}
	return &http.Response{
		StatusCode: statusCode, Body: io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)), Request: request,
	}, nil
}

func (f *fakeChanger) Validate(_ context.Context, request change.PreparedChange) error {
	index := f.validationCalls
	f.validationCalls++
	if request.SourceRecord != nil {
		f.sourceRecord = source.GitHubProvenance{Repository: request.SourceRecord.Repository, Release: request.SourceRecord.Release}
	}
	if f.validateErrAt > 0 && index == f.validateErrAt {
		return errors.New("simulated local preflight failure")
	}
	return nil
}

func (f *fakeChanger) Apply(_ context.Context, request change.PreparedChange) (change.Outcome, error) {
	if request.Kind != change.Uninstall && (f.stager == nil || f.stager.calls == 0) {
		return change.Outcome{}, errors.New("apply happened before batch preflight")
	}
	if f.planReady != nil && !*f.planReady {
		return change.Outcome{}, errors.New("apply happened before plan was reported")
	}
	f.requests = append(f.requests, request)
	index := len(f.requests) - 1
	if f.failIndex != nil && index == *f.failIndex {
		outcome := f.applyOutcome
		if outcome.PluginID == "" {
			outcome.PluginID = request.PluginID
		}
		return outcome, f.applyErr
	}
	if f.failAt > 0 && index == f.failAt {
		return change.Outcome{PluginID: request.PluginID, Restored: true}, errors.New("simulated replacement failure")
	}
	return change.Outcome{PluginID: request.PluginID, Changed: true}, nil
}

func (f *fakeChanger) pluginIDs() []string {
	ids := make([]string, len(f.requests))
	for index := range f.requests {
		ids[index] = f.requests[index].PluginID
	}
	return ids
}
