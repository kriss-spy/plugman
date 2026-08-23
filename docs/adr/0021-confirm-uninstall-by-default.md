# Confirm uninstall by default

Because uninstall deletes the entire plugin folder including `data.json`, Plugman displays the plugin version, enabled state, and presence of settings and asks once before deletion. `--keep-data` preserves settings, `--yes` explicitly skips confirmation, and non-interactive uninstall fails unless `--yes` is supplied.
