# Delete plugin data on uninstall by default

Plugman uninstall removes the community plugin's entire folder, including `data.json`, matching Obsidian's own uninstall semantics. A user may explicitly request preservation of plugin settings with a command flag; upgrades and reinstalls are not uninstalls and continue to preserve settings unless their own safety rules say otherwise.
