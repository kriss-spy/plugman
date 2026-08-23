# ADR 0029: Use the latest stable Obsidian release as the compatibility target

## Status

Accepted

## Context

Plugin release selection needs an Obsidian version, but a Vault does not record the desktop application's version. Running `obsidian version` while Obsidian is closed would launch the application and may execute installed plugins, violating read-only and preflight guarantees.

## Decision

Plugman reads Obsidian's published `desktop-releases.json` and uses its latest stable desktop version as the compatibility target. This lookup happens only after exact Vault Root validation and never starts Obsidian.

Plugman still reports each selected plugin's `minAppVersion`. A future non-launching, cross-platform way to determine the installed application version may refine this policy without changing Plugin Lists or Source Records.

## Consequences

Release resolution is deterministic and safe while the Vault is closed. A user intentionally running an older Obsidian build may need to update Obsidian before using a selected plugin release.
