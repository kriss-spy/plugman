# Plugman

Plugman is a standalone, vault-local package manager for Obsidian community plugins.

The CLI is implemented in Go and targets Linux, macOS, and Windows desktop.

## Install

Install once for your user account; the executable does not belong in a Vault.

macOS or Linux:

```sh
curl -fsSL https://github.com/kriss-spy/plugman/releases/latest/download/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://github.com/kriss-spy/plugman/releases/latest/download/install.ps1 | iex
```

Both installers select the correct CPU architecture, verify the release
checksum, install without administrator access, and add Plugman to the user
`PATH`. Open a new terminal afterward. Developers with Go installed may instead
run `go install github.com/kriss-spy/plugman/cmd/plugman@latest`.

To install without changing `PATH`, set `PLUGMAN_NO_MODIFY_PATH=1` before the
macOS/Linux command. In PowerShell, run
`$env:PLUGMAN_NO_MODIFY_PATH="1"` before the Windows command.

## Use

Run Plugman from the root of the Vault you want to manage:

```sh
plugman install dataview ./plugins.list
plugman install https://github.com/kriss-spy/obsidian-opencode
plugman update
plugman outdated
plugman export plugins.list
```

`install` accepts plugin IDs, Plugin List paths, and public GitHub repository or
release URLs. New plugins are disabled unless `--enable` is passed. When
Obsidian is open, its CLI must be installed and enabled so Plugman can safely
quiesce and reload plugins; closed-Vault operations do not require it.

`info` and `outdated` use bounded read-only metadata checks. Set `GH_TOKEN` (or
`GITHUB_TOKEN`) when resolving GitHub-only repositories to use authenticated
GitHub API limits; Plugman never writes or prints the token.

## Build locally

Use the Go version declared in `go.mod`. A normal development build is:

```sh
go build ./cmd/plugman
```

Release builds embed the tag in `plugman --version` and avoid local paths and
VCS metadata. To reproduce a Linux amd64 artifact locally:

```sh
VERSION=v0.1.0
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath \
  -buildvcs=false \
  -ldflags="-s -w -X main.version=${VERSION}" \
  -o "dist/plugman_${VERSION}_linux_amd64" \
  ./cmd/plugman
sha256sum "dist/plugman_${VERSION}_linux_amd64"
```

Set `GOOS` to `darwin` or `windows` and `GOARCH` to `amd64` or `arm64` for
the other release targets; Windows artifacts use an `.exe` suffix. Pushing a
`v*` tag runs the same build matrix, packages version-bound installers,
generates `checksums.txt`, and creates the GitHub Release. Do not reuse or move
a published tag.

- [Current product specification](docs/product-spec.md)
- [Architecture](docs/architecture.md)
- [Domain language](CONTEXT.md)
- [Decision records](docs/adr/)
- [Original research handoff](handoff-YINpVx.md)
