# Coordinate open-vault changes through Obsidian

Plugman will support mutating a vault while it is open, but it will coordinate affected plugins through Obsidian's disable, install or replace, reload, and re-enable lifecycle rather than replacing live files behind the application's back. If the running Obsidian version cannot be controlled safely, Plugman fails without changing the plugin; closed-vault operation remains available. This trades dependence on Obsidian integration for a safer live user experience.
