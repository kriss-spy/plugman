# Require explicit consent for plugin downgrades

An exact Plugin Declaration may upgrade an older installed plugin automatically, but Plugman will refuse to replace a newer version with an older one unless the user passes `--allow-downgrade`. Downgrades can be unsafe after a plugin migrates its settings or stored data, so the installed and requested versions must be shown before the user opts in.
