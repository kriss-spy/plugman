# Install GitHub Release assets only

For a GitHub repository URL, Plugman selects the newest compatible non-prerelease release and requires validated `main.js` and `manifest.json` assets, with optional `styles.css`. It never clones the repository, executes build scripts, or installs default-branch files; if no qualifying release exists, installation fails before the vault changes. This keeps direct GitHub installation predictable without executing repository-controlled build machinery.
