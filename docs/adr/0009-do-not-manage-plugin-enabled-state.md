---
status: superseded by 0009-do-not-enable-new-plugins-by-default
---

# Do not manage plugin enabled state

Plugman manages which community-plugin versions are installed but does not persistently enable or disable them; the user's Settings Management plugin owns that policy. During a live install or update, Plugman may ask Obsidian to reload an already-running plugin as a mechanical part of replacing it, but it preserves the vault's prior enabled state and provides no activation-management workflow.
