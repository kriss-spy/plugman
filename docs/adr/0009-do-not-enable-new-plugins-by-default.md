# Do not enable newly installed plugins by default

Installing a Community Plugin does not enable or execute it unless the user explicitly requests `--enable`. Updating a plugin preserves its prior enabled state, including the temporary disable and reload required for safe live updates. This matches Obsidian's separation of installation from trust and prevents shared Plugin Lists from silently executing new third-party code.
