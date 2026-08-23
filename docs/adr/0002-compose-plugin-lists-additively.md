# Compose Plugin Lists additively

`plugman install` requires one or more Plugin List paths and installs the union of their declarations into the current vault. Duplicate compatible declarations are accepted, conflicting version requirements fail before any changes, and plugins absent from the supplied lists are left untouched; removal remains explicit. This supports reusable lists without letting a partial list unexpectedly erase a vault's other plugins.
