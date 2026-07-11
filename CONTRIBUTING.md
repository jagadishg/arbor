# Contributing to Arbor

Thanks for helping improve Arbor. Bug reports, focused feature proposals, documentation fixes, and code contributions are welcome.

## Before opening a change

For significant behavior or workspace-format changes, open an issue first. Describe the user workflow, expected terminal interaction, file-format impact, and command-line equivalent where applicable.

## Development

Requirements:

- Go version declared in `go.mod` or newer
- a UTF-8 terminal for interactive testing

Run the standard checks:

```bash
make check
```

Changes should include tests at the lowest useful boundary. Use `httptest` for HTTP behavior, temporary directories for workspaces, and model-level tests for TUI state transitions.

## Pull requests

- Keep changes focused and explain the user-visible outcome.
- Preserve backward compatibility for version 1 workspace files.
- Never add real credentials, tokens, or captured private responses.
- Update documentation and examples when behavior changes.
- Confirm `make check` passes.

By contributing, you agree that your work is licensed under the repository's MIT license.
