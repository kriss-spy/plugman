# Support dry-run for every mutation

Install, update, and uninstall all accept `--dry-run`, which performs enough resolution and validation to report intended changes but does not disable or reload plugins, write vault files, alter enabled state, create Restore Points, or prompt for confirmation. This provides one predictable preview mechanism across the CLI.
