# Export Plugin Lists from vault state

`plugman export <path>` writes all installed plugins to a Plugin List with their exact installed versions, while `--enabled` includes only enabled plugins and `--latest` omits versions so future installs choose the newest compatible releases. Export refuses to overwrite an existing file unless explicitly requested, making the command a safe replacement for a generated lockfile when users want a portable snapshot.
