# Architecture

Arbor is structured so the terminal interface and command line share exactly the same behavior.

```text
CLI ─────────┐
             ├── application service ── HTTP executor
TUI ─────────┘            │
                          ├── workspace loader and validation
                          ├── scoped variable resolution
                          ├── secret providers
                          ├── assertions and extraction
                          └── scenario runner
```

## Boundaries

- `cmd/arbor` contains only process startup and exit handling.
- `internal/cli` owns command parsing and text or JSON presentation.
- `internal/tui` owns keyboard state and terminal rendering. Network operations are returned as Bubble Tea commands and never block the update loop.
- `internal/app` coordinates the use cases shared by both interfaces.
- `internal/workspace` discovers, strictly decodes, and validates local files.
- `internal/runtime` builds and executes HTTP requests.
- `internal/variables` implements scope precedence, interpolation, and redaction.
- `internal/secrets` integrates environment variables and native keychains.
- `internal/assertions`, `internal/responsevalue`, and `internal/scenario` implement test workflows.
- `internal/config` manages the central, user-level workspace registry (`$CONFIG/arbor/config.yaml`) and the last-used workspace; it is distinct from the per-workspace files, which remain the source of truth for a workspace's contents.
- `internal/model` contains the format and result types shared across those packages.

The filesystem is the source of truth. Arbor currently keeps no database and does not mutate request definitions during execution.

## Design rules

1. A behavior available in the TUI must remain usable non-interactively when that makes sense.
2. Request execution must be cancellable through `context.Context`.
3. Secrets must not be persisted in definitions, reports, URLs, or errors.
4. Workspace format changes require an explicit schema-version decision.
5. UI code does not parse YAML or construct HTTP requests directly.
