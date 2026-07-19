**Vercel Labs Skills CLI**

Reverse-Engineered Product and Technical Specification

**Repository: vercel-labs/skills**

Baseline: main branch, package version 1.5.19

Prepared 18 July 2026

> Independent specification derived from public documentation and source
> code. It is not an official Vercel specification. Normative language
> describes the observed implementation contract.

# 1. Executive Summary

The skills package is a cross-agent package manager and execution
utility for the open Agent Skills ecosystem. It accepts skill sources
from GitHub, GitLab, arbitrary Git remotes, well-known web sources, and
local directories; discovers valid SKILL.md packages; installs them into
one or more coding-agent-specific locations; tracks installed state;
supports listing, updating, and removal; and can generate or launch a
one-shot prompt without permanent installation.

The product is intentionally agent-neutral. Its primary abstraction is a
skill directory containing SKILL.md with YAML frontmatter. Agent support
is implemented as a registry mapping stable CLI identifiers to display
names, project paths, global paths, and installation-detection logic.

| **Dimension**    | **Specification**                                                                     |
|------------------|---------------------------------------------------------------------------------------|
| Product type     | Node.js command-line application and npm package                                      |
| Package name     | skills                                                                                |
| Runtime          | Node.js \>= 22.20.0                                                                   |
| Language         | TypeScript, ESM                                                                       |
| License          | MIT                                                                                   |
| Primary artifact | Skill directory containing SKILL.md                                                   |
| Primary users    | Developers using one or more AI coding agents                                         |
| Core value       | Portable discovery, installation, execution, and lifecycle management of agent skills |
| Observed version | 1.5.19                                                                                |

## 1.1 Design principles

- Portable by default: the same skill package should be installable into
  many agents.

- Repository-oriented: Git repositories are the principal distribution
  unit, but direct skill paths and local folders are supported.

- Convention over configuration: common skill directories and agent
  locations are built in.

- Interactive for humans, scriptable for automation: commands support
  prompts as well as non-interactive flags.

- Safe path handling: remote subpaths and installation paths are
  validated against traversal.

- Minimal skill contract: name and description in YAML frontmatter are
  sufficient for discovery.

- Single-source installation when possible: symlinks are preferred, with
  copying as a compatibility fallback.

# 2. Terminology and Normative Language

| **Term**        | **Meaning**                                                                                          |
|-----------------|------------------------------------------------------------------------------------------------------|
| Skill           | A directory whose root contains a valid SKILL.md file.                                               |
| Skill source    | A local path, repository reference, direct repository subpath, Git URL, or supported well-known URL. |
| Package         | A source that can contain one or multiple skills.                                                    |
| Agent           | A supported coding-agent product with configured skill installation paths.                           |
| Project scope   | Installation relative to the current working directory.                                              |
| Global scope    | Installation into a user-level agent directory.                                                      |
| Canonical copy  | The centrally copied skill used as the symlink source for one or more target agents.                 |
| Lockfile        | Machine-readable state describing installed skills and their sources.                                |
| Plugin manifest | Claude-compatible marketplace or plugin metadata declaring skill paths.                              |

MUST, MUST NOT, SHOULD, SHOULD NOT, and MAY indicate implementation
requirements in this reconstructed contract.

# 3. System Architecture

The application is organized as a command router over reusable modules.
The main pipeline is source parsing → source acquisition → skill
discovery → selection → target-agent resolution → installation or prompt
generation → state and telemetry finalization.

| **Component**      | **Responsibility**                                                                                                    |
|--------------------|-----------------------------------------------------------------------------------------------------------------------|
| CLI router         | Parses commands and command-specific flags; dispatches add, use, list, find, remove, update, init, install, and sync. |
| Source parser      | Normalizes shorthand, URLs, refs, subpaths, aliases, and local paths into ParsedSource.                               |
| Git/provider layer | Clones or fetches remote content and supports non-Git provider integrations.                                          |
| Skill discovery    | Finds SKILL.md files, parses frontmatter, applies visibility and depth rules, and deduplicates names.                 |
| Agent registry     | Defines agent IDs, display names, project/global paths, and installed-agent detection.                                |
| Installer          | Creates canonical skill copies and links or copies them into target directories.                                      |
| Lock subsystem     | Records source identity, selected skills, targets, and content hashes for lifecycle operations.                       |
| Lifecycle commands | List, remove, update, lock restore, and node_modules synchronization.                                                 |
| Telemetry          | Emits anonymous usage events unless disabled or running in CI.                                                        |

## 3.1 Core data model

> Skill {  
> name: string  
> description: string  
> path: string  
> rawContent?: string  
> pluginName?: string  
> metadata?: Record\<string, unknown\>  
> }  
>   
> ParsedSource {  
> type: "github" \| "gitlab" \| "git" \| "local" \| "well-known"  
> url: string  
> subpath?: string  
> localPath?: string  
> ref?: string  
> skillFilter?: string  
> }  
>   
> AgentConfig {  
> name: string  
> displayName: string  
> skillsDir: string  
> globalSkillsDir?: string  
> detectInstalled(): Promise\<boolean\>  
> }

# 4. Skill Package Specification

**SKILL-001 \[A\]** skill MUST be represented by a directory containing
a file named exactly SKILL.md.

**SKILL-002 \[SKILL.md\]** MUST begin with parseable YAML frontmatter
containing name and description.

**SKILL-003 \[name\]** and description MUST parse as strings; numeric,
boolean, array, or object values are invalid.

**SKILL-004 \[The\]** skill body MAY contain arbitrary Markdown
instructions and supporting references.

**SKILL-005 \[A\]** skill directory MAY contain additional files and
subdirectories; installation preserves the directory as the unit.

**SKILL-006 \[Skill\]** metadata MAY contain arbitrary structured
values. metadata.internal=true marks the skill as internal.

**SKILL-007 \[Internal\]** skills MUST be excluded from ordinary
discovery unless INSTALL_INTERNAL_SKILLS is 1 or true, or the skill is
explicitly requested.

**SKILL-008 \[Metadata\]** values exposed in terminal UI or telemetry
MUST be sanitized before use.

## 4.1 Minimal example

> ---  
> name: my-skill  
> description: What this skill does and when to use it  
> ---  
>   
> \# My Skill  
>   
> Instructions for the agent.  
>   
> \## When to use  
>   
> Describe activation scenarios.  
>   
> \## Instructions  
>   
> 1. Perform the first step.  
> 2. Validate the result.

## 4.2 Naming

The README recommends lowercase identifiers with hyphens. The
implementation performs case-insensitive exact selection and normalizes
spaces and underscores to hyphens in selected lock/discovery
comparisons. Names should be stable package identifiers; case-only
duplicates should be avoided, and the directory name should match the
frontmatter name.

# 5. Source Reference Grammar

| **Form**             | **Example**                                      | **Result**                                   |
|----------------------|--------------------------------------------------|----------------------------------------------|
| GitHub shorthand     | owner/repo                                       | GitHub repository                            |
| GitHub prefix        | github:owner/repo                                | GitHub repository                            |
| GitHub URL           | https://github.com/owner/repo                    | GitHub repository                            |
| GitHub tree path     | https://github.com/owner/repo/tree/main/skills/x | Repository + ref + subpath                   |
| GitLab prefix        | gitlab:group/repo                                | gitlab.com repository                        |
| GitLab URL           | https://gitlab.com/group/subgroup/repo           | GitLab repository                            |
| GitLab tree path     | https://host/group/repo/-/tree/main/path         | GitLab-compatible repository + ref + subpath |
| SSH Git              | git@github.com:owner/repo.git                    | Generic Git remote                           |
| Scheme SSH           | ssh://git@host:7999/owner/repo.git               | Generic Git remote                           |
| Local path           | ./skills or /absolute/path                       | Resolved local directory                     |
| Direct selection     | owner/repo@skill-name                            | Repository plus requested skill              |
| Fragment ref         | owner/repo#branch                                | Repository at explicit ref                   |
| Fragment ref + skill | owner/repo#branch@skill                          | Ref plus requested skill                     |

**SRC-001 \[MUST\]** resolve absolute and relative local paths before
interpreting repository shorthand.

**SRC-002 \[MAY\]** map deprecated or common source aliases to canonical
repositories.

**SRC-003 \[MUST\]** reject a repository subpath containing any path
segment equal to '..'.

**SRC-004 \[MUST\]** verify that a resolved subpath remains within the
acquired repository root.

**SRC-005 \[MUST\]** interpret ref fragments only for strings that look
like Git sources.

**SRC-006 \[MUST\]** preserve branch/ref and path information from
direct GitHub and GitLab tree URLs.

**SRC-007 \[SHOULD\]** preserve GitLab subgroup paths when deriving
source identity.

**SRC-008 \[MAY\]** return local paths as parsed sources before
existence validation; acquisition performs final validation.

# 6. Skill Discovery Algorithm

**DISC-001 \[MUST\]** attempt to parse SKILL.md at the selected search
root first.

**DISC-002 \[MUST\]** terminate at a valid root skill unless
--full-depth is enabled, except for recognized installed project copies.

**DISC-003 \[MUST\]** search standard containers,
curated/experimental/system directories, and registered agent project
paths.

**DISC-004 \[SHOULD\]** walk known containers one extra category level
for skills/\<category\>/\<skill\>/SKILL.md.

**DISC-005 \[MUST\]** let a shallower SKILL.md shadow nested skill
directories during ordinary scanning.

**DISC-006 \[MUST\]** fall back to recursive discovery when no skills
are found.

**DISC-007 \[MUST\]** perform recursive discovery when --full-depth is
set even if skills were already found.

**DISC-008 \[MUST\]** skip node_modules, .git, dist, build, and
\_\_pycache\_\_ during recursion.

**DISC-009 \[MUST\]** bound recursive depth; the observed implementation
uses five.

**DISC-010 \[MUST\]** deduplicate skills by frontmatter name.

**DISC-011 \[SHOULD\]** ignore an invalid or unreadable SKILL.md rather
than aborting the entire scan.

**DISC-012 \[MUST\]** include skill paths declared by supported plugin
manifests.

## 6.1 Plugin manifest compatibility

The CLI recognizes .claude-plugin/marketplace.json and
.claude-plugin/plugin.json. These manifests may define plugin roots,
plugin groupings, and explicit skill paths. Skills may be annotated with
their parent plugin name. Manifest-declared paths are searched at their
declared depth rather than receiving the ordinary catalog walk.

# 7. Agent Target Model

**AGENT-001 \[MUST\]** define every agent with a stable CLI ID, display
name, project path, optional global path, and detection function.

**AGENT-002 \[MUST\]** permit explicit agent selection with repeated or
variadic --agent values.

**AGENT-003 \[MUST\]** treat '\*' as all supported agents where agent
filters are accepted.

**AGENT-004 \[SHOULD\]** detect installed agents when no target is
explicitly selected.

**AGENT-005 \[SHOULD\]** prompt for targets when no installed agent is
detected in interactive mode.

**AGENT-006 \[MUST\]** reject or omit global installation for an agent
without a globalSkillsDir.

**AGENT-007 \[SHOULD\]** honor environment home overrides such as
CODEX_HOME and CLAUDE_CONFIG_DIR.

**AGENT-008 \[MAY\]** allow multiple agents to share one physical
project directory.

The observed type registry contains 73 CLI identifiers. Examples: Codex
and Cursor use .agents/skills at project scope; Claude Code uses
.claude/skills; OpenCode uses .opencode/skills; OpenClaw uses skills/.

# 8. Command-Line Interface

| **Command**          | **Aliases** | **Purpose**                                                           |
|----------------------|-------------|-----------------------------------------------------------------------|
| add \<source\>       | a           | Discover and install one or more skills.                              |
| use \<source\>       | —           | Generate a prompt or launch one agent without permanent installation. |
| list                 | ls          | List installed skills.                                                |
| find \[query\]       | —           | Search skill catalogs interactively or by keyword.                    |
| remove \[skills...\] | rm          | Remove installed skills.                                              |
| update \[skills...\] | upgrade     | Update installed skills from source.                                  |
| init \[name\]        | —           | Create a SKILL.md starter.                                            |
| experimental_install | —           | Restore installation from skills-lock.json.                           |
| experimental_sync    | —           | Sync skills from node_modules into agent directories.                 |

## 8.1 add

**ADD-001 \[MUST\]** accept one source package argument.

**ADD-002 \[MAY\]** accept repeated or multiple --skill values for exact
selection.

**ADD-003 \[MUST\]** make --list discover and display skills without
installing.

**ADD-004 \[MUST\]** use project scope by default and global scope with
--global.

**ADD-005 \[MUST\]** force independent copies with --copy.

**ADD-006 \[MUST\]** suppress confirmation prompts with --yes when
choices are complete.

**ADD-007 \[MUST\]** treat --all as --skill '\*' --agent '\*' --yes.

**ADD-008 \[MUST\]** pass --full-depth into discovery.

**ADD-009 \[MUST\]** validate --metadata as JSON.

**ADD-010 \[SHOULD\]** report unknown exact skill selections clearly.

## 8.2 use

**USE-001 \[MUST\]** share source and discovery semantics with add.

**USE-002 \[MUST\]** select one skill through @skill syntax or --skill.

**USE-003 \[MUST\]** write only the generated prompt to stdout when
--agent is absent.

**USE-004 \[MUST\]** launch exactly one supported agent when --agent is
provided.

**USE-005 \[MUST\]** use ephemeral files and avoid installed-state
registration.

**USE-006 \[MAY\]** require explicit acknowledgement for unverified
OpenClaw community skills.

## 8.3 list

**LIST-001 \[MUST\]** list project-scope skills by default.

**LIST-002 \[MUST\]** switch to global scope with --global.

**LIST-003 \[MUST\]** filter by --agent.

**LIST-004 \[MUST\]** emit parseable, ANSI-free JSON with --json.

**LIST-005 \[SHOULD\]** consolidate shared physical paths while
retaining agent associations.

## 8.4 find

**FIND-001 \[SHOULD\]** provide interactive search when no query is
given.

**FIND-002 \[MUST\]** perform keyword search when a query is given.

**FIND-003 \[MUST\]** constrain repositories with --owner.

**FIND-004 \[SHOULD\]** return source identifiers directly usable by
add.

## 8.5 remove

**RM-001 \[SHOULD\]** offer interactive selection when no skill is
explicit.

**RM-002 \[MUST\]** accept both positional names and --skill values.

**RM-003 \[MUST\]** limit --global removal to global scope.

**RM-004 \[MUST\]** constrain removal by --agent.

**RM-005 \[MUST\]** support '\*' for skills and agents.

**RM-006 \[MUST\]** treat --all as --skill '\*' --agent '\*' --yes.

**RM-007 \[MUST\]** update tracked state and clean unreferenced managed
copies.

**RM-008 \[MUST NOT\]** delete unrelated user content.

## 8.6 update

**UPD-001 \[MUST\]** target all installed skills in scope when no names
are given.

**UPD-002 \[MUST\]** restrict updates to matching positional names when
supplied.

**UPD-003 \[MUST\]** support mutually exclusive --global and --project
scope filters.

**UPD-004 \[SHOULD\]** prompt for scope in ambiguous interactive use.

**UPD-005 \[SHOULD\]** auto-detect project scope under --yes when
tracked project state exists, otherwise global.

**UPD-006 \[MUST\]** re-resolve the original source while preserving
targets.

**UPD-007 \[SHOULD\]** compare content hashes.

**UPD-008 \[SHOULD\]** report partial failures per skill.

## 8.7 init

**INIT-001 \[MUST\]** create SKILL.md in the current directory and
derive the name from its basename when no name is given.

**INIT-002 \[MUST\]** create \<name\>/SKILL.md recursively when a name
is given.

**INIT-003 \[MUST NOT\]** overwrite an existing SKILL.md.

**INIT-004 \[MUST\]** generate required frontmatter and instructional
placeholders.

## 8.8 experimental commands

**EXP-001 \[MUST\]** make experimental_install read skills-lock.json and
restore represented installations.

**EXP-002 \[MUST\]** make experimental_sync discover
node_modules-provided skills and synchronize selected targets.

**EXP-003 \[SHOULD\]** visibly label experimental commands and allow
incompatible evolution.

# 9. Installation Semantics

Interactive installation prefers a canonical-copy plus symlink model,
allowing one source of truth to serve multiple agent directories. Copy
mode creates independent directories for environments where symlinks are
unsupported or undesirable.

**INST-001 \[MUST\]** create required parent directories recursively.

**INST-002 \[SHOULD\]** use symlinks for default project installation
where supported.

**INST-003 \[MUST\]** perform recursive copies and create no symlinks
under --copy.

**INST-004 \[MAY\]** target multiple agents in one operation.

**INST-005 \[MUST\]** avoid conflicting duplicate writes when targets
share a path.

**INST-006 \[MUST\]** handle existing destinations deterministically.

**INST-007 \[MUST\]** use safe installation directory names.

**INST-008 \[SHOULD\]** make project installations suitable for version
control.

**INST-009 \[MUST\]** root global installations in configured user
paths.

**INST-010 \[MUST\]** report selected skills, scope, targets, and
method.

| **Scope** | **Default root**                               | **Intended behavior**                    |
|-----------|------------------------------------------------|------------------------------------------|
| Project   | Current working directory + agent project path | Shareable with repository and team.      |
| Global    | Agent-specific user path                       | Available across projects for that user. |

# 10. Installation State and Locking

The repository contains project and local lock mechanisms. The exact
schema may evolve, but lifecycle behavior requires sufficient source and
target information to restore, update, list, and remove skills reliably.

**LOCK-001 \[MUST\]** identify each tracked skill by a stable normalized
name.

**LOCK-002 \[SHOULD\]** record source, subpath, ref, and selected skill
where applicable.

**LOCK-003 \[SHOULD\]** record scope, method, and target agents or
paths.

**LOCK-004 \[SHOULD\]** record a content hash.

**LOCK-005 \[MUST\]** write state atomically or
interruption-resiliently.

**LOCK-006 \[MUST\]** avoid re-importing already-installed project
copies during source discovery.

**LOCK-007 \[SHOULD\]** ignore unknown future fields.

**LOCK-008 \[MUST\]** fail safely on malformed lockfiles without
destructive cleanup.

> {  
> "version": 1,  
> "skills": {  
> "web-design-guidelines": {  
> "source": "vercel-labs/agent-skills",  
> "ref": "main",  
> "subpath": "skills/web-design-guidelines",  
> "hash": "\<content hash\>",  
> "scope": "project",  
> "method": "symlink",  
> "agents": \["claude-code", "codex"\]  
> }  
> }  
> }

Illustrative logical schema only; not a byte-for-byte claim about the
current lockfile.

# 11. Security and Trust Requirements

**SEC-001 \[MUST\]** reject explicit '..' source subpath components.

**SEC-002 \[MUST\]** verify resolved skill paths remain within the
source root.

**SEC-003 \[MUST\]** sanitize installation names and generated
destinations.

**SEC-004 \[MUST NOT\]** allow source-controlled paths to write outside
intended targets.

**SEC-005 \[SHOULD\]** display source identity because remote skills are
untrusted instructions.

**SEC-006 \[MAY\]** require explicit danger acknowledgement for risky
agent-specific execution.

**SEC-007 \[MUST NOT\]** expose private-repository credentials or
content through telemetry.

**SEC-008 \[MUST\]** exclude secrets, file contents, and authentication
tokens from telemetry.

**SEC-009 \[MUST\]** limit destructive operations to managed paths.

**SEC-010 \[MUST\]** fail safely on malformed YAML, JSON, manifests, and
lockfiles.

**SEC-011 \[MUST\]** guard symlink replacement against redirection
outside managed directories.

**SEC-012 \[SHOULD\]** use unpredictable temporary directories and
remove them after use.

## 11.1 Trust boundary

Installing a skill places instructions into an agent's trusted discovery
path. A malicious skill can influence an agent to execute commands,
disclose data, or modify code. Filesystem validation does not prove
semantic safety of Markdown instructions; repository trust and source
review remain user responsibilities.

# 12. Telemetry and Privacy

**TEL-001 \[MAY\]** enable anonymous usage telemetry by default.

**TEL-002 \[MUST\]** disable telemetry when DISABLE_TELEMETRY is set.

**TEL-003 \[MUST\]** disable telemetry when DO_NOT_TRACK is set.

**TEL-004 \[MUST\]** disable telemetry automatically in recognized CI
environments.

**TEL-005 \[MUST\]** validate user-supplied --metadata as JSON.

**TEL-006 \[MUST NOT\]** allow telemetry failure to fail the primary
command.

# 13. Error Handling and Exit Behavior

**ERR-001 \[MUST\]** produce command-specific help or concise
diagnostics for invalid syntax.

**ERR-002 \[MUST\]** make --help and --version succeed without source
access.

**ERR-003 \[MUST\]** explain common causes when no valid skills are
found.

**ERR-004 \[MUST\]** identify unavailable sources without printing
credentials.

**ERR-005 \[MUST\]** identify the affected path on permission failure.

**ERR-006 \[MUST\]** identify unsupported agent IDs and valid
alternatives.

**ERR-007 \[MUST\]** fail rather than hang when non-interactive input
lacks required choices.

**ERR-008 \[SHOULD\]** keep stdout machine-readable in JSON mode and
send diagnostics to stderr.

**ERR-009 \[SHOULD\]** return non-zero when no requested operation
occurs due to invalid selection.

**ERR-010 \[SHOULD\]** clean temporary resources after success or
failure.

# 14. Agent Compatibility Model

| **Feature**          | **Compatibility expectation**                                                |
|----------------------|------------------------------------------------------------------------------|
| Basic Markdown skill | Broadly portable across all registered agents.                               |
| allowed-tools        | Supported by many agents, but not universally.                               |
| context: fork        | Observed as Claude Code-specific in the README compatibility table.          |
| Hooks                | Supported only by selected agents.                                           |
| Supporting files     | Generally copied; whether they are loaded is agent-specific.                 |
| Global scope         | Unavailable for agents without a global directory.                           |
| Auto-loading         | Agent-specific; some custom-agent configurations require explicit resources. |

**COMP-001 \[MUST\]** preserve skill content rather than deleting
unsupported frontmatter.

**COMP-002 \[SHOULD\]** warn only when incompatibility is known and
actionable.

**COMP-003 \[SHOULD\]** keep existing CLI agent IDs backward compatible.

**COMP-004 \[SHOULD\]** use .agents/skills when the target supports the
ecosystem-standard path.

# 15. Non-Functional Requirements

**NFR-001 \[MUST\]** run on Node.js 22.20.0 or newer.

**NFR-002 \[MUST\]** support macOS, Linux, and Windows path conventions.

**NFR-003 \[SHOULD\]** remain usable through npx without global
installation.

**NFR-004 \[SHOULD\]** remain legible on light and dark terminals.

**NFR-005 \[MUST\]** avoid ANSI escapes in machine-readable output.

**NFR-006 \[SHOULD\]** parallelize independent directory reads where
safe.

**NFR-007 \[SHOULD\]** bound ordinary discovery to avoid expensive
recursion.

**NFR-008 \[SHOULD\]** tolerate unreadable optional directories.

**NFR-009 \[MUST\]** publish as ESM and expose skills and add-skill
executables.

**NFR-010 \[SHOULD\]** provide reproducible build, type-check, format,
and test scripts.

# 16. Reference Workflows

## 16.1 Multi-agent installation

> npx skills add vercel-labs/agent-skills \\  
> --skill frontend-design \\  
> --skill web-design-guidelines \\  
> --agent codex \\  
> --agent claude-code

1.  Parse the GitHub shorthand.

2.  Acquire the repository into a temporary working directory.

3.  Discover valid skills in standard locations.

4.  Resolve the exact requested names.

5.  Resolve project paths for both agents.

6.  Create canonical copies and links, unless copy mode is selected.

7.  Write state and print a summary.

## 16.2 One-shot prompt

> npx skills use vercel-labs/agent-skills@web-design-guidelines \|
> claude

The command resolves one skill, constructs a prompt referencing
temporary files, prints only that prompt, and leaves no installed state.

## 16.3 CI installation

> npx skills add vercel-labs/agent-skills \\  
> --skill frontend-design --global \\  
> --agent claude-code --yes --copy

## 16.4 Restore from lock

> npx skills experimental_install

# 17. Acceptance Test Matrix

| **ID** | **Scenario**        | **Expected result**                                                       |
|--------|---------------------|---------------------------------------------------------------------------|
| AT-001 | Minimal local skill | Valid local name/description is discovered and installable.               |
| AT-002 | Invalid field types | Non-string name or description is rejected without crashing.              |
| AT-003 | Internal hidden     | internal=true is hidden by default and visible with the environment flag. |
| AT-004 | GitHub shorthand    | owner/repo resolves to canonical GitHub source.                           |
| AT-005 | GitHub tree path    | Branch and subpath are retained.                                          |
| AT-006 | Traversal rejection | A subpath containing ../ is rejected.                                     |
| AT-007 | Root shadowing      | Root SKILL.md returns alone unless --full-depth.                          |
| AT-008 | Catalog depth       | skills/category/name/SKILL.md is discovered normally.                     |
| AT-009 | Recursive fallback  | A nonstandard nested skill is found when standard search finds none.      |
| AT-010 | Deduplication       | Duplicate frontmatter names produce one selectable skill.                 |
| AT-011 | Exact selection     | --skill is case-insensitive exact matching.                               |
| AT-012 | Multi-agent install | One skill installs to two targets without duplicate canonical content.    |
| AT-013 | Copy mode           | --copy creates directories, not symlinks.                                 |
| AT-014 | Global unsupported  | Project-only agent rejects global installation.                           |
| AT-015 | JSON list           | list --json emits parseable ANSI-free JSON.                               |
| AT-016 | Noninteractive add  | Fully specified add -y does not read stdin.                               |
| AT-017 | Use stdout purity   | use without --agent emits only the prompt.                                |
| AT-018 | Init no overwrite   | Existing SKILL.md is preserved.                                           |
| AT-019 | Remove isolation    | Removing one skill leaves unrelated files untouched.                      |
| AT-020 | Update unchanged    | Unchanged hash is reported as current.                                    |
| AT-021 | Telemetry opt-out   | Both opt-out environment variables suppress telemetry.                    |
| AT-022 | CI telemetry        | CI suppresses telemetry.                                                  |
| AT-023 | Malformed lock      | Malformed lock fails without deleting installations.                      |
| AT-024 | Plugin manifest     | Declared paths are discovered and grouping is retained.                   |

# 18. Extensibility

## 18.1 Adding an agent

8.  Add an AgentType literal.

9.  Add an AgentConfig entry with name, display name, project path,
    global path or undefined, and detection logic.

10. Add its project path to discovery locations when applicable.

11. Update generated documentation tables and tests.

12. Verify add, list, remove, update, wildcard, and scope behavior.

## 18.2 Adding a provider

RemoteSkill and provider registry modules indicate an extension model
beyond Git cloning. A provider returns display name, description, full
content, install name, source URL, provider ID, source identifier, and
optional metadata.

**EXT-001 \[MUST\]** select a provider deterministically for a source
URL.

**EXT-002 \[MUST NOT\]** silently reinterpret arbitrary content after
provider failure.

**EXT-003 \[MUST\]** route provider skills through the same sanitization
and lifecycle pipeline.

# 19. Known Ambiguities and Inferred Behavior

| **Area**                 | **Confidence** | **Comment**                                                                            |
|--------------------------|----------------|----------------------------------------------------------------------------------------|
| CLI names and flags      | High           | Explicit in README and cli.ts.                                                         |
| Skill validity/discovery | High           | Explicit in README and skills.ts.                                                      |
| Source syntax/security   | High           | Explicit in source-parser.ts and skills.ts.                                            |
| Agent registry           | High           | Explicit in types.ts and agents.ts; the list changes frequently.                       |
| Symlink canonical layout | Medium–High    | Method is documented; low-level layout is implementation-specific.                     |
| Exact lockfile JSON      | Medium         | Logical needs are clear; exact internal schema is intentionally not frozen here.       |
| Every exit code          | Medium         | Requirements follow standard CLI behavior; numeric codes were not exhaustively traced. |
| Provider protocol        | Medium         | Provider modules exist; no complete external protocol is asserted.                     |
| OpenClaw risk gate       | Medium         | The flag exists; trust policy may evolve.                                              |

# 20. Source Baseline

Derived from the public main branch of vercel-labs/skills inspected on
18 July 2026. package.json reported version 1.5.19 and Node.js
\>=22.20.0.

| **Repository file**  | **Use**                                                                                                          |
|----------------------|------------------------------------------------------------------------------------------------------------------|
| README.md            | Commands, flags, examples, installation methods, skill format, discovery, compatibility, environment, telemetry. |
| package.json         | Identity, version, runtime, scripts, dependencies, ESM/bin distribution, license.                                |
| src/cli.ts           | Router, help surface, init behavior, aliases.                                                                    |
| src/types.ts         | Core data contracts.                                                                                             |
| src/source-parser.ts | Source grammar, aliases, refs, subpaths, traversal checks.                                                       |
| src/skills.ts        | Validation, visibility, discovery order/depth, plugin grouping, deduplication.                                   |
| src/agents.ts        | Agent paths, environment overrides, detection.                                                                   |

Canonical repository: https://github.com/vercel-labs/skills

# Appendix A. Consolidated CLI Grammar

> skills add \<source\> \[--global\] \[--agent \<id...\>\] \[--skill
> \<name...\>\]  
> \[--list\] \[--copy\] \[--yes\] \[--all\] \[--full-depth\]  
> \[--metadata \<json\>\] \[--subagent \<name...\>\]  
>   
> skills use \<source\> \[--skill \<name\>\] \[--agent \<id\>\]
> \[--full-depth\]  
> \[--dangerously-accept-openclaw-risks\]  
>   
> skills list\|ls \[--global\] \[--agent \<id...\>\] \[--json\]  
> skills find \[query\] \[--owner \<owner\>\]  
> skills remove\|rm \[skills...\] \[--global\] \[--agent \<id...\>\]  
> \[--skill \<name...\>\] \[--yes\] \[--all\]  
> skills update\|upgrade \[skills...\] \[--global \| --project\]
> \[--yes\]  
> skills init \[name\]  
> skills experimental_install  
> skills experimental_sync \[--agent \<id...\>\] \[--yes\]  
> skills --help  
> skills --version

# Appendix B. Recommended Stable Public Contract

Downstream integrations should depend on command names and documented
flags; source forms; SKILL.md name/description requirements;
project/global semantics; JSON list output; environment opt-outs; and
non-destructive lifecycle behavior. They should not depend on temporary
directory names, internal canonical-copy paths, terminal colors, prompt
wording, undocumented lock fields, or agent detection order.
