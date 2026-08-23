# Treat Plugin Lists as read-only inputs

Plugman reads but never edits Plugin Lists. Install and update commands change the current vault, while uninstalling a plugin does not remove its declaration from any list; installing that list again may therefore restore it. This keeps reusable, shared, and composed lists under explicit user control.
