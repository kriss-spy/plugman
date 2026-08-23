# Treat update as explicit consent

`plugman update` applies its preflighted changes without an additional confirmation prompt, following familiar package-manager behavior. It prints the planned version changes before mutation and supports `--dry-run` for a no-change preview; confirmation remains appropriate for destructive operations such as uninstall and downgrade.
