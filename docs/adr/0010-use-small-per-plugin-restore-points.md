# Use small per-plugin restore points

Before changing a plugin, Plugman records a short-lived Restore Point containing its folder and prior enabled state, then coordinates the live transition with Obsidian. A controlled failure restores that plugin, and a later Plugman run detects an interrupted change and offers recovery. Restore Points are not a batch transaction, permanent backup history, or a promise to undo plugin-driven changes to notes, remote services, Sync, or migrated data beyond the captured plugin folder.
