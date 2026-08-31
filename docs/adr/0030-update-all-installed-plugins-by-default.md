# Update all installed plugins by default

Bare `plugman update` updates every installed plugin with a resolvable source, not only plugins recognized by Obsidian's official community directory. A plugin recognized by the official directory is updated through that directory, and a GitHub-only plugin is updated through its retained Source Record, matching the checkable set reported by `plugman outdated`. A new `--enabled` flag limits the bare update to enabled plugins; targeted updates supplied as explicit inputs are unaffected.

This supersedes ADR-0014, which deliberately excluded GitHub-only plugins from a bare update to mirror Obsidian's official-directory update-all behavior. Retaining Source Records for GitHub installs makes them resolvable without requiring users to re-supply URLs, and aligning `update` with `outdated` removes a surprising gap between the decision step and the update step.
