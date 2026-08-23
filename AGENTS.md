# Repository Guidelines

## Project Structure & Module Organization

Treat `docs/product-spec.md` as the user-behavior contract and `docs/architecture.md` as the implementation map. Use canonical domain terms from `CONTEXT.md`; durable decisions live in `docs/adr/`. Historical research in `handoff-YINpVx.md` and `vercel-skills-full-spec.md` is reference material, not current scope.

The Go implementation follows the architecture’s modular-monolith layout: CLI wiring under `cmd/plugman/` and internal modules for manager, source, vault, change, model, and reporting under `internal/`. Keep Go tests beside the packages they exercise.

## Build, Test, and Development Commands

- `git diff --check` — catch whitespace errors in every change.
- `gofmt -w $(find cmd internal -type f -name '*.go')` — format all Go sources.
- `go build ./cmd/plugman` — build the CLI after the Go module is scaffolded.
- `go test ./...` — run the complete Go test suite.
- `go test -race ./...` — check concurrent and live-vault code for races.

Do not claim a command passed if its required scaffold or dependency does not exist.

## Coding Style & Naming Conventions

Use `gofmt`; do not align Go manually. Package names are short lowercase nouns. Exported identifiers use PascalCase; internal identifiers use camelCase. Preserve the deep-module seams in the architecture and avoid shallow pass-through packages. Use glossary terms such as **Vault**, **Plugin Input**, and **Restore Point** consistently.

## Testing Guidelines

Test observable behavior through module interfaces. Prefer table-driven tests, temporary real Vault directories, and deterministic source adapters. Name tests `Test<Behavior>`. Mutations require coverage for success, preflight failure, partial failure, restoration, and interrupted recovery where applicable.

## Commit & Pull Request Guidelines

Use concise imperative subjects, for example `Add plugin-list parser`. Pull requests must link a GitHub issue, describe user-visible behavior, list verification commands, and identify ADR or compatibility impacts. Include representative terminal output for CLI behavior changes.

## Security & Configuration

Never execute plugin repository build scripts or log secrets. Validate plugin IDs, release assets, compatibility, and filesystem paths before modifying a Vault.

## Agent skills

### Issue tracker

Work is tracked in GitHub Issues for `kriss-spy/plugman`. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the five configured triage-role labels. See `docs/agents/triage-labels.md`.

### Domain docs

This is a single-context repository. See `docs/agents/domain.md`.
