# Plugman

Plugman manages the community plugins belonging to one Obsidian vault through an npm-like command-line experience.

## Language

**Vault**:
An Obsidian notes collection whose community plugins are managed together.
_Avoid_: Project, workspace

**Vault Root**:
The top-level directory of a **Vault** and the directory from which Plugman is run.
_Avoid_: Project root, working directory

**Community Plugin**:
An installable Obsidian extension identified by its community-plugin ID.
_Avoid_: Package, dependency, skill

**Plugin List**:
A user-selected file declaring **Community Plugins** to install in the current **Vault**.
_Avoid_: Manifest, package file, plugin manifest

**Plugin Declaration**:
A line in a **Plugin List** containing an official plugin ID with an optional exact version, a GitHub repository URL, or an exact GitHub release URL.
_Avoid_: Dependency, package entry

**Plugin State**:
The installed files, settings, and Obsidian enabled state of one **Community Plugin** in one **Vault**.
_Avoid_: Plugin folder, installation files

**Restore Point**:
A short-lived snapshot of a plugin's prior **Plugin State** used to recover from an interrupted or failed change.
_Avoid_: Backup, lockfile

**Plugin Input**:
A direct official plugin ID, Plugin List path, or explicit GitHub URL supplied to an install or targeted update command.
_Avoid_: Path, source, Install Input

**Source Record**:
Per-plugin metadata identifying the GitHub repository and release from which a **Community Plugin** was installed.
_Avoid_: Lockfile, Plugin List

## Relationships

- A **Vault Root** identifies exactly one **Vault**
- A **Vault** has zero or more **Community Plugins**
- A **Vault** may be configured from one or more **Plugin Lists**
- A **Plugin List** contains zero or more **Plugin Declarations**
- A valid **Plugin Declaration** resolves to exactly one **Community Plugin**
- A **Community Plugin** may be present in multiple **Vaults**, with each vault managed independently
- A **Community Plugin** has at most one current **Plugin State** per **Vault**
- A **Restore Point** belongs to exactly one **Community Plugin** change
- A **Plugin Input** identifies either one **Plugin List** or one **Community Plugin**
- A GitHub-installed **Community Plugin** may have one **Source Record** in its plugin folder

## Example dialogue

> **User:** "Which vault will `plugman` change?"
> **Plugman:** "The **Vault** whose **Vault Root** you ran it from."
> **User:** "Which plugins will it install?"
> **Plugman:** "Those declared by the **Plugin Lists** you pass to `plugman install`."

## Flagged ambiguities

- "project" can mean a software repository or an Obsidian vault; Plugman uses **Vault** for the managed unit.
- "plugin" can mean Plugman itself or an installed extension; Plugman uses **Community Plugin** for the extensions it manages.
- "manifest" already names Obsidian plugin metadata; Plugman uses **Plugin List** for its user-authored input files.
- "installed" was initially discussed as static files; **Plugin State** also includes settings and Obsidian's enabled state.
- "path" initially meant a local **Plugin List**, but commands also accept direct official IDs and GitHub URLs; all three are **Plugin Inputs**.
- A **Source Record** remembers provenance only; it does not select or lock a version.
