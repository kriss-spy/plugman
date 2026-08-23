# Issue Tracker: GitHub

Issues and implementation tickets live in GitHub Issues for `kriss-spy/plugman`. Use the `gh` CLI from this repository so it infers the remote.

## Conventions

- Create: `gh issue create --title "..." --body "..."`
- Read with comments: `gh issue view <number> --comments`
- List: `gh issue list --state open`
- Comment: `gh issue comment <number> --body "..."`
- Label: `gh issue edit <number> --add-label "..."`
- Close: `gh issue close <number> --comment "..."`

When a skill says to publish a ticket, create one GitHub issue. Use native blocking relationships if available; otherwise include a **Blocked by** section with issue references. Do not close or modify a parent issue unless explicitly requested.
