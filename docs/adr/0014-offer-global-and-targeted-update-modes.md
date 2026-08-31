# Offer global and targeted update modes

Bare `plugman update` updates all installed plugins, while `plugman update <Plugin Inputs...>` updates only plugins resolved from those IDs, lists, or GitHub URLs. Exact declarations remain fixed. The scope of the bare update was widened by ADR-0030 to include GitHub-only plugins resolved through their retained Source Records. If Obsidian's private live-update surface is incompatible, the global operation fails safely rather than editing loaded files directly.

> Superseded by ADR-0030.
