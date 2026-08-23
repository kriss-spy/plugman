# Require the current directory to be the Vault Root

Plugman operates only when the current directory is the exact Obsidian Vault Root and contains its configuration directory. It does not search parent directories or accept a global vault-path override in the first release. This stricter-than-npm rule makes the target of installation, update, and uninstall operations explicit.
