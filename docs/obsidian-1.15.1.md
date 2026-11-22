# Obsidian 1.15.1 compatibility check

Issue: [#21](https://github.com/kriss-spy/plugman/issues/21).

Obsidian's [1.15.1 release notes](https://obsidian.md/changelog/2026-11-12-desktop-v1.15.1/)
describe two Community Plugin changes. Installation retains pre-existing settings
as `data.json.bak`. The settings screen skips manifests without `name`.

## Settings during installation

Plugman selects only `main.js`, `manifest.json`, and optional `styles.css` from
GitHub releases. It does not download `data.json`, `data.json.bak`, or repository
source files. Settings bundled in a release therefore cannot become active
settings through Plugman installation.

If a plugin folder already exists with only settings, Plugman refuses installation
before replacing files. It leaves those settings unchanged. This behavior also
applies after `uninstall --keep-data`. Plugman does not automatically rename
existing settings to `data.json.bak` or adopt them on a fresh installation.

Updates preserve the installed plugin's `data.json`. This remains separate from
fresh installation.

Verification:

- `TestReleaseAssetsExcludeBundledSettings` checks release asset selection.
- `TestStageDoesNotDownloadBundledSettings` checks the staged files.
- `TestInstallRefusesToAdoptSettingsWithoutInstalledPlugin` checks preflight
  failure and unchanged settings in a temporary Vault.
- `TestApplyUpdatePreservesDataAndRemovesStaleStyles` checks update preservation.

## Manifest names

The official and GitHub resolvers already require a non-empty manifest `name`.
Staging now repeats this check on the downloaded asset. This prevents a changed
release asset from bypassing the earlier check.

Verification: `TestGitHubResolverRejectsNamelessManifest` checks resolution.
`TestStageRejectsManifestMismatchAndCleansPartialDestination` checks staging and
cleanup after rejection.

Plugman's local inspector continues to expose folder identity and version facts.
It does not copy Obsidian's settings-screen filtering.

## Verification scope

These checks exercise deterministic HTTP fixtures and temporary Vault directories.
They do not verify live changes against a running Obsidian 1.15.1 instance.
Live runtime support still depends on Plugman's runtime probe and contract tests.
