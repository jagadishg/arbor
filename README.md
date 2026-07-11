# Arbor

**A terminal-native, local-first API workspace.**

Arbor brings the speed and familiarity of k9s to API development. Browse collections, switch environments, run requests, inspect responses, and execute test scenarios without leaving the terminal. Every workspace is made of readable YAML files that belong in Git.

```text
 ARBOR   Acme API                                              env: staging
 Requests                     Get current user
 3 items
                              GET  {{base_url}}/users/me
 › users.me       GET
   users.create   POST        Reference: users.me
   users.delete   DELETE      File: collections/users/me.yaml

                              Assertions
                                • status == 200
                                • body.id != null

 Ready                 [enter] run  [e] edit  [/] filter  [:] command  [?] help
```

## Why Arbor?

- **Terminal native:** fast keyboard workflows, Vim navigation, fuzzy filtering, command mode, and contextual help.
- **Local first:** no account, cloud workspace, or background service.
- **Git friendly:** requests, environments, and scenarios are ordinary YAML files.
- **One engine everywhere:** the TUI, command line, and CI execute the same definitions.
- **Secrets stay secret:** resolve values from environment variables or the operating system keychain, with automatic redaction.
- **Composable tests:** assertions and extracted values turn requests into repeatable API scenarios.

## Installation

Install with Go:

```bash
go install github.com/jagadishg/arbor/cmd/arbor@latest
```

Tagged releases also provide standalone archives for macOS, Linux, and Windows on the [GitHub releases page](https://github.com/jagadishg/arbor/releases).

Arbor requires a terminal with UTF-8 and ANSI color support. Windows Terminal is recommended on Windows.

## Quick start

```bash
mkdir my-api && cd my-api
arbor init --name "My API"
arbor new request health.get --url '{{base_url}}/health'
arbor
```

Inside Arbor:

- `j` / `k` or arrow keys move through resources.
- `Enter` runs the selected request or scenario.
- `1`, `2`, and `3` open requests, scenarios, and environments.
- `/` filters the current resource list.
- `:` opens command mode.
- `e` opens the selected YAML file in `$EDITOR`.
- `?` shows contextual help.
- `q` quits.

Run the included example without opening the TUI:

```bash
cd examples/github
arbor validate
arbor run github.user
arbor scenario github.profile
```

## Workspace layout

```text
my-api/
├── arbor.yaml
├── collections/
│   └── users/
│       ├── get.yaml
│       └── create.yaml
├── environments/
│   ├── local.yaml
│   └── staging.yaml
└── scenarios/
    └── user-lifecycle.yaml
```

`arbor.yaml` marks the workspace root:

```yaml
version: 1
name: My API
defaultEnvironment: local

variables:
  api_version: v1

http:
  timeout: 30s
```

A request is a versioned YAML file anywhere under `collections/`:

```yaml
version: 1
kind: request
id: users.get
name: Get user
method: GET
url: "{{base_url}}/{{api_version}}/users/{{user_id}}"

headers:
  Accept: application/json
  Authorization: "Bearer {{token}}"

query:
  include: profile

assert:
  - status == 200
  - body.id == "{{user_id}}"

extract:
  email: body.email
```

See [Workspace format](docs/workspace-format.md) for the full schema and precedence rules.

## Environments and secrets

Environment values override workspace values:

```yaml
version: 1
kind: environment
name: staging

variables:
  base_url: https://staging.example.com

secrets:
  token: keychain://arbor-acme/staging-token
  legacy_key: env://ACME_LEGACY_KEY
```

Store a declared keychain secret without placing it in shell history:

```bash
arbor secret set token --env staging
```

Values resolve in this order, with later scopes winning:

```text
workspace → environment → scenario → extracted values → --var
```

Resolved secrets are redacted from URLs and execution errors. Secret values are never written into workspace files or reports.

## Scenarios

Scenarios chain requests and carry extracted values forward:

```yaml
version: 1
kind: scenario
id: auth.smoke
name: Authentication smoke test

steps:
  - request: auth.login
    extract:
      token: body.access_token

  - request: users.me
    assert:
      - status == 200
      - body.email contains "@"
```

Run one interactively or in CI:

```bash
arbor scenario auth.smoke --env staging
```

The command exits non-zero when transport, extraction, or assertion failures occur.

## Command line

```text
arbor                              Open the interactive workspace
arbor init                         Initialize a workspace
arbor new request <ref>            Create a request
arbor new environment <name>       Create an environment
arbor new scenario <ref>           Create a scenario
arbor validate                     Validate all workspace files
arbor list requests                List request references
arbor list environments            List environments
arbor list scenarios               List scenario references
arbor run <ref>                    Run one request
arbor scenario <ref>               Run a scenario
arbor secret set <name>            Store a keychain secret
arbor secret delete <name>         Delete a keychain secret
```

Runtime variables can be supplied without editing files:

```bash
arbor run users.get --env staging --var user_id=123
```

Use `--json` with `arbor run` for machine-readable output.

## Commands inside the TUI

```text
:requests                 Open requests
:scenarios                Open scenarios
:environments             Open environments
:use staging              Switch environment
:run users.get            Run a request by reference
:run auth.smoke           Run a scenario by reference
:reload                   Reload files from disk
:help                     Open keyboard help
:quit                     Quit Arbor
```

Arbor also reloads the workspace after returning from `$EDITOR`. Use `Ctrl-r` after any other external edit.

## Development

```bash
make test
make build
./bin/arbor --help
```

The core request engine does not depend on the TUI, so behavior can be tested without a terminal. See [Architecture](docs/architecture.md) and [Contributing](CONTRIBUTING.md) before proposing larger changes.

## Status

Arbor is an early project. Version 1 of the workspace format covers HTTP/JSON requests, environments, keychain and environment-variable secrets, assertions, extraction, and sequential scenarios. The format is versioned so future protocol and workflow features can evolve without silently changing existing workspaces.

## License

[MIT](LICENSE)
