# Plugman Product Specification

Status: accepted product contract for the first implementation.

## Product promise

Plugman is a standalone, npm-like command-line manager for Obsidian community plugins. A command operates on exactly one Vault: the Vault Root that is the current directory.

Plugman manages Plugin State rather than treating plugins as static folders. It works while Obsidian is open by coordinating disable, replacement, reload, and re-enable behavior with Obsidian. It also works while Obsidian is closed.

The first release supports Linux, macOS, and Windows desktop.

## Vault selection

- The current directory must be the exact Vault Root.
- The Vault Root must contain its Obsidian configuration directory, initially `.obsidian`.
- Plugman does not search parent directories.
- Plugman does not accept a vault-path override in the first release.
- A validation failure must occur before any mutation.

## Plugin Inputs

Install and targeted update commands accept one or more Plugin Inputs in any combination:

```text
dataview
dataview@0.5.67
base.plugins
https://github.com/kriss-spy/obsidian-opencode
https://github.com/kriss-spy/obsidian-opencode/releases/tag/1.3.13
```

An existing local file is a Plugin List. Supported GitHub URLs are GitHub inputs. Remaining valid identifiers are official community-plugin IDs. File-like arguments that do not exist must produce a missing-file error rather than silently becoming plugin IDs.

### Plugin Lists

A Plugin List is a user-owned, read-only text file:

```text
# Core plugins
dataview
homepage@1.4.3
https://github.com/kriss-spy/obsidian-opencode
```

- Blank lines and `#` comments are ignored.
- An official ID may include an exact `@version`.
- A GitHub repository URL requests the newest compatible non-prerelease release.
- A GitHub exact-release URL requests that release, including a prerelease if explicitly selected.
- Version ranges are not supported initially.
- Plugman never edits a Plugin List.
- Multiple lists compose additively.
- Compatible duplicates are deduplicated by the resolved manifest ID.
- Conflicting exact versions fail before any vault change.
- Plugins absent from supplied lists are left untouched.

## Command surface

### `plugman install <inputs...>`

Options: `--enable`, `--allow-downgrade`, `--dry-run`.

- At least one Plugin Input is required.
- A missing unversioned plugin is installed at the newest compatible release.
- An already-installed unversioned plugin is left unchanged.
- An exact declaration upgrades an older installed version.
- An exact declaration cannot downgrade a newer installed version without `--allow-downgrade`.
- Newly installed plugins remain disabled unless `--enable` is supplied.
- `--enable` applies to plugins newly installed by this command; it does not enable unrelated or already-installed disabled plugins.

### `plugman update [inputs...]`

Options: `--enabled`, `--allow-downgrade`, `--dry-run`.

- With no inputs, update every installed plugin with a resolvable source: official community directory or GitHub Source Record.
- `--enabled` limits a bare update to enabled plugins.
- With inputs, update only the plugins resolved from those inputs.
- An unversioned official ID or GitHub repository advances to the newest compatible non-prerelease release.
- Exact IDs and exact release URLs remain fixed.
- A GitHub-only plugin is updated through its Source Record; a plugin recognized by the official directory is updated through the official directory.
- Enabled plugins remain enabled; disabled plugins remain disabled.
- The command prints its planned version changes and applies them without another confirmation.

### `plugman uninstall <ids...>`

Options: `--keep-data`, `--yes`, `--dry-run`.

- The default removes the entire plugin folder, including `data.json` and the Source Record.
- `--keep-data` preserves `data.json` while removing installed plugin code and metadata.
- Interactive use shows version, enabled state, and whether `data.json` exists, then asks once for confirmation.
- Non-interactive use requires `--yes`.
- Uninstall never edits Plugin Lists.

### `plugman list`

Options: `--enabled`, `--json`.

- Lists installed plugins using vault-local state only.
- Reports plugin ID, version, enabled state, and known source.
- `--enabled` filters to enabled plugins.
- `--json` emits stable machine-readable output and no ANSI escapes.

### `plugman outdated`

Options: `--enabled`, `--json`.

- Checks every installed official plugin and every GitHub plugin with a Source Record.
- Reports current version, newest compatible version, enabled state, source, and release URL.
- `--enabled` filters to enabled plugins.
- Makes no changes.
- Does not generate AI summaries.

### `plugman info <input>`

Option: `--json`.

- Accepts one official ID or GitHub URL.
- Reports identity, release and compatibility information, desktop-only status, source, installation state, and release URL.
- Makes no changes.

### `plugman export <path>`

Options: `--enabled`, `--latest`, `--force`.

- Exports all installed plugins to a Plugin List using exact installed versions.
- `--enabled` exports enabled plugins only.
- `--latest` omits exact versions or exact-release selection.
- Official plugins export as IDs; GitHub-only plugins export using their Source Records.
- If an included plugin is neither official nor accompanied by a Source Record, export fails before writing and reports the unresolved plugin.
- An existing destination is not overwritten without `--force`.

### Common behavior

- `--dry-run` on every mutating command may resolve and validate but cannot alter Plugin State or create recovery artifacts.
- Bare `plugman` displays help.
- `plugman --version` displays the executable version.
- Progress and diagnostics go to stderr when structured output is requested.

## GitHub installations

- Only public `https://github.com/<owner>/<repo>` repository and release URLs are supported initially.
- Plugman installs GitHub Release assets only.
- A valid release requires `main.js` and `manifest.json`; `styles.css` is optional.
- Plugman never clones the repository, builds source, or runs repository build scripts.
- The downloaded manifest ID and version must agree with the resolved release.
- Obsidian version and desktop compatibility are validated before mutation.
- A GitHub install writes `.plugman.json` inside the plugin folder with repository and selected-release provenance.
- The Source Record is not a lockfile and does not constrain future resolution.

## Enabled state

- Installation and execution permission remain separate.
- New plugins are disabled by default.
- `install --enable` explicitly enables new plugins.
- Update preserves prior enabled state.
- Live replacement may temporarily disable a plugin as a mechanical safety step.

## Compatibility target

Because a Vault does not record the installed Obsidian desktop version, Plugman selects releases against Obsidian's latest published stable desktop version. It obtains that version without launching Obsidian and reports the selected plugin's minimum required Obsidian version.

## Failure and recovery behavior

1. Parse, expand, resolve, validate, and download the entire requested batch.
2. If preflight fails, change nothing.
3. Apply plugins sequentially.
4. Before changing one plugin, create a short-lived Restore Point for its Plugin State.
5. If that plugin fails, restore it and stop the command.
6. Keep plugins completed earlier in the batch.
7. Report successes, the failure, and restoration outcome.
8. A rerun skips satisfied inputs and resumes naturally.

Plugman does not promise to undo changes that plugin code makes to notes, external services, synchronized devices, or data outside the captured plugin folder.

## Stable result categories

- Success: the request completed or no change was needed.
- Preflight failure: no Plugin State changed.
- Partial failure: earlier plugins succeeded and the failing plugin was restored.
- Recovery required: Plugman could not verify restoration and preserved recovery material for manual action.

Numeric exit codes and JSON schemas will be frozen with the first implementation rather than in this design-only repository.

## Explicitly outside the first release

- Lockfiles or desired-state reconciliation
- AI-generated update briefs
- Official-directory text search
- Mobile executables
- Private repositories or GitHub authentication workflows
- GitLab, arbitrary Git remotes, BRAT metadata, or local source builds
- Version ranges, channels, or implicit prerelease selection
- Permanent backup history or whole-batch rollback
- A community-plugin user interface
- Automatic self-update
