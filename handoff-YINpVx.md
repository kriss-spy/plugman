# Handoff: Obsidian Community Plugin Package Manager

Date: 2026-07-18

## Next-session goal

Settle the product and technical design for a general-purpose package manager that improves the Obsidian community-plugin management experience. Do not jump directly into implementation. Resolve product boundaries, state/lockfile design, version resolution, installation backends, safety guarantees, and the role of AI-generated update briefs.

## Motivation

Obsidian provides an **Update all** button, but users incur significant cognitive debt:

- Reading every plugin's release notes is impractical.
- Some projects publish notes only on GitHub.
- The newest release note does not cover all changes when the installed version is several releases behind.
- Existing CLI installation is useful for bootstrapping a vault, but it does not provide reproducibility or update intelligence.

The initial use case was to compare each installed plugin version with the latest compatible version, collect all changes across the skipped-version interval, and produce a concise user-facing feature/bug-fix/risk brief. This evolved into a broader idea: a package manager for Obsidian community plugins, with update briefing as one subcommand.

## Existing bootstrap workflow

The user currently knows plugins can be installed into a vault with the official CLI:

```bash
plugins=(
  obsidian-style-settings
  darlal-switcher-plus
  quick-explorer
  easy-typing-obsidian
  scroll-with-jk
  table-editor-obsidian
  dataview
  obsidian-terminal
  homepage
)

for id in "${plugins[@]}"; do
  obsidian plugin:install id=$id enable vault="vault-name"
done
```

This installs whatever Obsidian currently resolves, but it cannot express a reproducible desired state.

## Verified official CLI capabilities

`obsidian help` was run locally. The current CLI supports:

- `plugins versions`
- `plugins:enabled versions`
- `plugin id=<id>`
- `plugin:install id=<id> enable`
- `plugin:enable`, `plugin:disable`, `plugin:reload`, `plugin:uninstall`
- `version`, `vault`, `vaults`
- `eval code=<javascript>`
- Console/error inspection and restart commands

It does **not** expose dedicated commands for:

- Updating one or all plugins
- Checking outdated plugins
- Installing a requested plugin version
- Version constraints or pinning
- Release notes or cumulative changelogs
- Lockfiles
- Integrity verification
- Transactions or rollback

Conclusion: shell scripts around the official CLI are adequate for simple bootstrap, but the CLI alone is not a package-management engine.

## Verified official plugin distribution protocol

Primary sources:

- https://github.com/obsidianmd/obsidian-releases/blob/master/README.md
- https://docs.obsidian.md/Plugins/Releasing/Submit+your+plugin
- https://docs.obsidian.md/Reference/Versions
- Registry: https://community.obsidian.md/assets/community-plugins.json

Protocol:

1. The official registry maps plugin IDs to GitHub `owner/repository` identifiers.
2. Obsidian reads root `manifest.json` from the repository to determine the latest version.
3. If that manifest requires a newer Obsidian version, Obsidian consults root `versions.json` for a compatible fallback.
4. The selected GitHub release tag must exactly match the plugin version, normally without a `v` prefix.
5. Obsidian downloads release assets named `main.js`, `manifest.json`, and optional `styles.css`.
6. Assets are stored under the vault configuration directory's `plugins/<plugin-id>/` directory.

`versions.json` maps plugin versions to minimum Obsidian versions, but maintainers only need entries when `minAppVersion` changes. It is not necessarily a complete release index.

## Verified `obsidian eval` runtime surface

Read-only inspection of `app.plugins` found these current private methods:

```text
loadManifests
loadManifest
loadPlugin
unloadPlugin
enablePlugin
disablePlugin
enablePluginAndSave
disablePluginAndSave
uninstallPlugin
getPlugin
saveConfig
installPlugin
checkForUpdates
```

Relevant state includes:

```text
app.plugins.manifests
app.plugins.plugins
app.plugins.enabledPlugins
app.plugins.updates
app.plugins.lastUpdateCheck
app.vault.configDir
```

Inspection of the current minified implementation established:

- `await app.plugins.checkForUpdates(false)` populates `app.plugins.updates`.
- Each update is shaped approximately as `{ repo, version, manifest }`.
- The current installer signature is effectively `installPlugin(repo, version, manifest)`.
- It downloads the exact release's assets, writes them into the plugin directory, loads the manifest, and reloads a currently loaded plugin.
- Installing a specific version appears technically possible if the caller supplies the repository, exact version, and expected manifest.

Potential discovery call:

```bash
obsidian eval code="await app.plugins.checkForUpdates(false); JSON.stringify(app.plugins.updates)"
```

Important limitation: these are private, minified implementation details, not stable documented APIs. They can change between Obsidian releases and currently include UI notice behavior.

## Current architecture direction

Do not build the entire product as one agent workflow or one large `obsidian eval` script. The emerging recommendation is:

```text
Standalone deterministic package-manager core
├── official registry cache
├── repository and release resolution
├── compatibility resolution
├── version constraints and lockfile
├── artifact download and validation
├── transaction journal, backups, and rollback
├── release-evidence collection and cache
└── replaceable execution backends
    ├── Obsidian runtime/eval backend
    └── direct filesystem backend

Optional AI layer
└── synthesize collected evidence into a user-facing update brief
```

The package manager must remain deterministic and useful without an agent. AI should summarize evidence, not resolve versions, select trusted artifacts, or perform the critical installation transaction.

### Runtime/eval backend

Benefits:

- Uses Obsidian's own current installation behavior.
- Can disable, install, enable, and reload plugins in the running application.
- Can verify runtime state and inspect console errors afterward.

Costs:

- Requires Obsidian to be running.
- Relies on private unstable APIs.
- Is unsuitable as the only headless provisioning path.
- Needs version-gated adapter contract tests and safe failure behavior.

### Direct filesystem backend

Potential responsibilities:

- Download release assets into staging.
- Validate manifest ID, selected version, `minAppVersion`, and platform compatibility.
- Record hashes.
- Back up the current installation.
- Atomically replace `main.js`, `manifest.json`, and `styles.css`.
- Preserve plugin `data.json` by default.
- Remove stale `styles.css` when a new release omits it.
- Restore the previous installation on failure.

This backend would support closed-app/headless provisioning. Enabling/disabling plugins while Obsidian is running should preferably go through Obsidian rather than modifying its configuration behind its back.

## Candidate command surface

Names are provisional; `obpm` was used as a placeholder.

```bash
obpm search "table"
obpm info dataview
obpm list
obpm outdated

obpm add dataview
obpm add dataview@0.5.67
obpm remove dataview

obpm plan --upgrade-all
obpm upgrade dataview
obpm upgrade --all
obpm upgrade --safe
obpm upgrade --dry-run

obpm brief
obpm brief dataview

obpm lock
obpm apply
obpm rollback dataview
obpm rollback --last-transaction
```

`plan --upgrade-all` is a likely centerpiece: resolve updates, classify risk, summarize cumulative user-visible changes, and separate safe batches from updates requiring review.

## Update-brief concept

For installed `1.2.0` and target `1.7.0`, analyze the complete interval:

```text
1.2.0 < versions <= 1.7.0
```

Evidence cascade:

1. Every GitHub release body in the interval
2. Repository changelog/release-note files
3. Tag-to-tag GitHub comparison
4. Associated merged pull requests
5. Commits
6. Source diff when documentation is incomplete

The collection phase should be deterministic, parallel, cached, and machine-readable. AI synthesis should classify:

- New user-facing features
- Behavior changes
- Relevant bug fixes
- Breaking changes or migrations
- Compatibility, mobile, security, filesystem, network, and data risks
- Documentation coverage and claim-level confidence

Exclude CI, formatting, tests, dependency bumps, and internal refactors unless they affect users, compatibility, security, or performance.

The product should keep two artifacts conceptually separate:

- A short decision brief for the user
- An evidence ledger with versions, tags, SHAs, links, gaps, and confidence

## Candidate desired-state model

Provisional manifest:

```toml
[plugins]
obsidian-style-settings = "*"
darlal-switcher-plus = "*"
dataview = "0.5"
homepage = "1.4.3"
```

Provisional lock entry:

```toml
[[plugin]]
id = "dataview"
repo = "blacksmithgu/obsidian-dataview"
version = "0.5.67"
min_obsidian = "0.13.11"
main_js_sha256 = "..."
manifest_sha256 = "..."
```

The design must distinguish desired constraints, resolved versions, and actual per-vault installed state.

## Safety requirements already identified

- Validate downloaded manifest ID and version.
- Enforce Obsidian compatibility and desktop/mobile constraints.
- Stage before changing installed files.
- Preserve settings by default.
- Record hashes and source provenance.
- Back up or journal every mutation.
- Define rollback before installation.
- Detect missing/mismatched release assets.
- Fail closed when tags, manifests, releases, or registry entries disagree.
- Account for data migrations that cannot be reversed merely by restoring plugin binaries.
- Avoid treating Sync as a backup.

## Open design decisions

Resolve these before implementation:

1. Is the primary product a standalone CLI, an Obsidian plugin UI, or a CLI core with an optional plugin frontend?
2. What is the source of truth: a desired-state manifest, current vault state, or both?
3. Where should manifest, lockfile, transaction journal, caches, and backups live?
4. Should lockfiles be portable across desktop/mobile and differing Obsidian versions, or should resolution be platform-specific?
5. What version-constraint syntax is needed initially: exact, wildcard, semver ranges, channels/prereleases?
6. How should official-directory, BRAT, manual, forked, private, and archived plugins differ?
7. Should the runtime/eval backend ship first, or should the direct backend define canonical install semantics from the start?
8. What guarantees can be made for atomic replacement on each supported OS?
9. How should enabled state and plugin settings be modeled without fighting a running Obsidian instance or Sync?
10. How are repository transfers and registry changes represented in the lockfile?
11. How should GitHub authentication, ETags, rate limits, pagination, and offline caches work?
12. What exact risk policy powers `upgrade --safe`? Avoid presenting heuristic classification as a security guarantee.
13. Should update briefs be generated locally by a configured model, delegated to an agent, or exported as evidence for any summarizer?
14. What is the minimum viable product and what should explicitly be deferred?
15. What language/runtime best fits a fast, portable, single-binary utility while keeping GitHub and semver implementation practical?

## Suggested next-session approach

1. Use `codebase-design` to define deep module boundaries and stable interfaces.
2. Use `domain-modeling` to establish precise terms such as package, plugin, source, release, resolution, target, desired state, installed state, transaction, and evidence claim.
3. Use `grill-me` or `grilling` to force decisions on product scope and failure semantics.
4. Use `product-manager` only if prioritizing an MVP and user workflows needs separate treatment.
5. Use `research` if additional primary-source verification is needed before choosing installation semantics.

No implementation files, PRD, ADR, issue, or repository changes were created during this brainstorm. This handoff is the sole artifact from the session.
