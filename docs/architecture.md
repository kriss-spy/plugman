# Plugman Architecture

Status: implementation architecture for the first release.

## Architectural shape

Plugman is one Go executable organized around a deep package-manager module. The CLI is a thin caller: it converts arguments into an operation, invokes the module, and renders its report. Product behavior, ordering, recovery, and error classification stay behind that module's interface so both callers and tests exercise the same seam.

```text
CLI
 └─ Package Manager
     ├─ Input expansion and planning
     ├─ Vault inspection
     ├─ Release resolution
     │   ├─ Official-directory adapter
     │   └─ GitHub-release adapter
     └─ Plugin change engine
         ├─ Recovery store
         └─ Vault adapter
             ├─ Live Obsidian adapter
             └─ Closed-vault adapter
```

The application is a modular monolith. There is no daemon, local server, plugin companion, database, or background agent.

## Technology choice

Go is the implementation language. It supports one executable per desktop platform, direct cross-compilation, bounded concurrency, subprocess control, and the HTTP/JSON/filesystem work Plugman requires without imposing a runtime installation on users.

The initial implementation should prefer the Go standard library. A third-party dependency is justified only when it removes protocol or operating-system complexity that would otherwise leak across modules, such as portable advisory file locking or well-tested semantic-version comparison.

## External interface

The package-manager module exposes one conceptual entry point:

```go
type Manager interface {
    Run(context.Context, Operation) (Report, error)
}
```

`Operation` is a closed set of install, update, uninstall, list, outdated, info, and export requests. `Report` contains ordered observations and outcomes independent of terminal formatting.

The interface guarantees:

- Vault validation precedes network access or mutation.
- Mutating operations perform full-batch preflight before application.
- Application ordering is deterministic by first declaration occurrence.
- At most one plugin is in a transitional state at a time.
- Every mutation returns a verified final state or a recovery-required result.
- Cancellation is honored during preflight; during mutation it is deferred until the active plugin is stable or restored.

The CLI owns prompts, terminal color, table layout, and JSON serialization. The Manager owns whether confirmation is required and returns the facts needed to ask it; confirmed execution is a second request carrying explicit consent.

## Core modules

### Package Manager

This is the deepest module and the primary test surface. It hides:

- Plugin Input classification and Plugin List expansion
- Duplicate and conflict detection
- Current-state comparison
- Release selection and compatibility rules
- Whole-batch preflight
- Per-plugin application ordering
- Dry-run behavior
- Outcome and recovery classification

Deleting this module would spread command semantics across every CLI handler, so it earns the seam.

### Release Resolver

The Release Resolver turns a Plugin Declaration plus Vault facts into one validated Release:

```go
type ReleaseSource interface {
    Resolve(context.Context, Declaration, Target) (Release, error)
}
```

This is a real internal seam with two production adapters:

- The official-directory adapter maps plugin IDs to repositories and resolves Obsidian-compatible releases.
- The GitHub-release adapter resolves explicit repositories and exact release URLs.

Tests use an in-memory adapter. Callers never handle GitHub pagination, ETags, tag normalization, `versions.json`, release-asset naming, or repository transfers.

Every resolved Release contains normalized plugin identity, selected version, compatibility metadata, provenance, and staged asset references. Resolution does not mutate a Vault.

### Vault Inspector

Vault inspection reads local manifests, enabled IDs, Source Records, the Obsidian configuration directory, and any interrupted recovery journal into a coherent snapshot. Other modules do not read `.obsidian` structures independently.

The inspector treats malformed installed folders as explicit state rather than silently ignoring them, allowing list, export, preflight, and recovery to report the same facts.

### Plugin Change Engine

The change engine owns the state transition for exactly one plugin:

```go
type ChangeEngine interface {
    Apply(context.Context, PreparedChange) (ChangeOutcome, error)
}
```

It creates a Restore Point, delegates the transition to the selected Vault adapter, verifies the resulting Plugin State, and deletes recovery material only after verification. It restores automatically on a controlled failure.

This module hides staging layout, atomic replacement mechanics, stale `styles.css` removal, Source Record persistence, enabled-state restoration, and recovery-journal transitions.

### Vault adapters

The Vault seam exists because two materially different behaviors are required.

The live Obsidian adapter:

- Probes the running Obsidian runtime before mutation.
- Refuses unsupported private-runtime shapes before changing files.
- Disables or unloads the affected plugin when necessary.
- Replaces validated staged assets only while the plugin is quiescent.
- Reloads manifests and plugin code through Obsidian.
- Restores the prior enabled state.
- Verifies runtime and disk state afterward.

The closed-vault adapter:

- Replaces validated staged assets using same-filesystem atomic renames where available.
- Updates enabled configuration only when explicitly requested.
- Verifies disk state afterward.

There is no fallback from a failed live-runtime probe to raw live replacement. A test adapter exercises the Change Engine without Obsidian.

### Recovery Store

Recovery is intentionally narrow. Plugman takes an exclusive per-vault operation lock and maintains at most one active per-plugin journal under:

```text
.obsidian/.plugman/recovery/current/
```

The journal records operation identity, plugin ID, prior enabled state, prior folder presence, and transition phase. The Restore Point contains the previous plugin folder when one existed.

- Successful verification deletes the journal and Restore Point immediately.
- A verified automatic restore also deletes them.
- Process termination or power loss leaves the current journal intact.
- Before any later mutation, Plugman completes or restores the interrupted plugin first.
- If recovery cannot be verified, Plugman refuses further mutation and reports the retained recovery path.

There is no recovery history, retention policy, batch snapshot, or general backup command.

## Operation pipeline

Every mutation follows the same pipeline:

```text
validate Vault Root
→ acquire per-vault lock
→ recover interrupted plugin if necessary
→ classify and expand Plugin Inputs
→ inspect current Plugin State
→ resolve and validate all Releases
→ download all required assets to staging
→ build and print deterministic plan
→ if dry-run, stop
→ apply one plugin
→ verify or restore it
→ repeat until complete or first failure
→ report final outcomes
```

Downloads occur before the first mutation. Staging lives on the same filesystem as the plugin directory when atomic rename semantics require it. Temporary and recovery paths must never be placed inside an individual plugin folder.

## State and persistence

Plugman creates no manifest, lockfile, database, or long-lived transaction history.

Persistent state is limited to:

- The plugin files Obsidian already uses
- Obsidian's enabled-plugin configuration
- `.plugman.json` inside GitHub-installed plugin folders

Ephemeral state is limited to staging, the per-vault operation lock, and the current Restore Point. Cache data may use the operating system's user cache directory, is always disposable, and never participates in correctness.

## Concurrency and Obsidian coordination

Only one Plugman mutation may run in a Vault at once. Read-only commands may run concurrently unless recovery is pending.

The operation lock protects Plugman from another Plugman process. It does not claim to freeze Obsidian or Sync. The live adapter must re-check enabled and loaded state immediately before each transition because Obsidian can change between planning and application. If the observed plugin identity or version no longer matches the plan, that plugin fails without replacement.

## Network behavior

The official registry and GitHub are true external dependencies. Production uses HTTP adapters; tests use deterministic local adapters.

- Conditional requests and bounded parallel downloads are internal optimizations.
- GitHub rate limits and transport failures remain preflight failures.
- Cached metadata may improve availability but cannot turn an unknown or stale release into a valid target.
- Plugman never executes downloaded plugin code during preflight.

## Security invariants

- Only HTTPS GitHub URLs are accepted initially.
- Repository source trees and build scripts are never executed.
- Manifest ID, release version, minimum Obsidian version, and desktop-only metadata are validated.
- Required assets must be present and regular files.
- Archive or path traversal is rejected even if future asset formats introduce archives.
- Asset replacement cannot follow symlinks outside the plugin directory.
- Restore and staging paths are derived from validated plugin IDs, never raw input strings.
- Secrets are not written to Plugin Lists, Source Records, logs, or recovery journals.

## Testing strategy

Tests cross the same interfaces used by the CLI.

1. Package-manager acceptance tests use an in-memory Release Source and temporary real Vault filesystem.
2. Change-engine contract tests run against both the closed-vault adapter and a fake live Obsidian adapter.
3. Live-adapter contract tests run against supported Obsidian versions and verify probe, disable, replacement, reload, enable, and restoration behavior.
4. Crash tests terminate the process at each journal phase and verify next-run recovery.
5. Filesystem tests run on Linux, macOS, and Windows for rename, locks, permissions, stale styles, and path handling.
6. GitHub/registry fixture tests cover missing assets, tag/manifest mismatch, incomplete `versions.json`, transfers, removals, prereleases, and rate limits.

Tests assert observable Reports and Vault state rather than internal call sequences.

## Suggested package layout

The layout is a starting point, not an interface commitment:

```text
cmd/plugman/             CLI wiring and rendering
internal/manager/        deep Package Manager implementation
internal/source/         Release Resolver and production adapters
internal/vault/          inspection and Vault adapters
internal/change/         Plugin Change Engine and recovery
internal/model/          domain values shared across deep modules
internal/report/         terminal and JSON renderers
```

Do not split parsing, validation, downloading, staging, and compatibility into shallow pass-through packages. Keep them inside the module that owns their invariant until a second real adapter justifies a seam.
