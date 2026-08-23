# Record GitHub provenance per plugin

When Plugman installs from GitHub, it writes a small `.plugman.json` Source Record inside that plugin's folder containing the repository and selected release. Obsidian ignores this file; Plugman preserves it across updates and removes it with the folder on uninstall. The record enables targeted updates and reinstallable exports but does not constrain resolution or act as a lockfile.
