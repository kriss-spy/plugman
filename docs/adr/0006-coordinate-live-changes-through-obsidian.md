# Coordinate open-vault changes through Obsidian

Plugman will support mutating a vault while it is open, but it will coordinate affected plugins through Obsidian's disable, install or replace, reload, and re-enable lifecycle rather than replacing live files behind the application's back. If the running Obsidian version cannot be controlled safely, Plugman fails without changing the plugin; closed-vault operation remains available. This trades dependence on Obsidian integration for a safer live user experience.

Live coordination applies only to the vault Obsidian is currently focused on. When Obsidian is running but focused on a different vault, the target vault is not open in any running application, so Plugman treats it as closed and applies filesystem-only changes after emitting a warning. This keeps a second vault installable without closing Obsidian, while still refusing unsafe live coordination when the target vault is actually open.
