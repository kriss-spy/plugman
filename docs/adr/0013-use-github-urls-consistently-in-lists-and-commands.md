# Use GitHub URLs consistently in lists and commands

Plugin Lists and direct `install` commands accept the same GitHub forms: a repository URL selects its newest compatible non-prerelease release, while a `/releases/tag/<version>` URL selects that exact release. Using ordinary URLs avoids a Plugman-specific repository/version syntax and lets a one-off installation move into a reusable Plugin List unchanged.
