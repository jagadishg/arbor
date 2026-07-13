# Arbor

**A terminal-native, local-first API workspace.**

Arbor brings the speed and familiarity of k9s to API development. Browse collections, switch environments, run requests, inspect responses, and execute test scenarios without leaving the terminal. Every workspace is made of readable YAML files that belong in Git.

**[▶ See the live demo and docs →](https://jagadishg.github.io/arbor/)**

<p align="center">
  <img src="docs/demo/tui.gif" alt="Arbor terminal UI: browse a request, run it, and inspect the split request/response view" width="820" />
</p>

Create a workspace, add a request, and run it — all from the command line:

<p align="center">
  <img src="docs/demo/cli.gif" alt="Creating an Arbor workspace, collection, and request, then running it from the CLI" width="820" />
</p>

## Why Arbor?

- **Terminal native:** fast keyboard workflows, Vim navigation, fuzzy filtering, command mode, and contextual help.
- **Local first:** no account, cloud workspace, or background service.
- **Git friendly:** requests, environments, and scenarios are ordinary YAML files.
- **One engine everywhere:** the TUI, command line, and CI execute the same definitions.
- **Secrets stay secret:** resolve values from environment variables or the operating system keychain, with automatic redaction.
- **Composable tests:** assertions and extracted values turn requests into repeatable API scenarios.
- **Agent ready:** descriptions on every resource and `--json` output make a workspace legible to coding agents as well as people.

## Installation

Install with Homebrew (macOS):

```bash
brew install jagadishg/tap/arbor
```

Or install with Go:

```bash
go install github.com/jagadishg/arbor/cmd/arbor@latest
```

When Arbor starts interactively, it checks GitHub for a newer stable release at
most once per day and prints upgrade instructions when one is available. The
check is best-effort and never runs for commands such as `arbor validate` or in
CI. Set `ARBOR_NO_UPDATE_CHECK=1` to disable it.

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

- `j` / `k` or arrow keys move through resources; `g` / `G` jump to the first or last row.
- `Enter` or `d` describes the selected resource, just as it does in k9s; in the collections view `Enter` drills into that collection's requests.
- `r` runs the selected request or scenario; `l` shows its last response.
- Running a request opens a **split view** — request on the left, response on the right (focused by default). `Tab` (or `h`/`l`) switches focus, `j`/`k` scroll the focused pane; the response shows a colored status line, timing, assertions, headers, and a syntax-highlighted body.
- The request pane shows the **actual request that was sent** — resolved URL, headers, and body — with secrets redacted. `x` reveals or hides secrets, and `y` toggles between the sent request and the raw YAML definition (the edit target). `e` opens the definition in `$EDITOR`.
- `c` copies to the clipboard, following the focused pane: the raw response body from the response pane, or the request as a runnable `curl` command from the request pane (secrets are redacted unless revealed with `x`).
- `:` opens an alias-aware command prompt at the top of the screen with an autocomplete list; `Tab`, `Ctrl-f`, or `→` accepts a suggestion.
- `Ctrl-a` lists resource aliases; `Esc` or `h` returns to the previous view.
- `/` filters the current resource view incrementally; `?` shows help; `q` or `Esc` goes back; `Ctrl-c` quits.

Run the included example without opening the TUI:

```bash
cd examples/github
arbor validate
arbor run github.user
arbor scenario github.profile
```

`examples/httpbin` is a second, credential-free workspace against
[httpbin.org](https://httpbin.org) with collections for every HTTP method, common status
codes, and request inspection:

```bash
cd examples/httpbin
arbor run methods.post      # GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS all covered
arbor scenario httpbin.smoke
```

## Workspace layout

```text
my-api/
└── .arbor/
    ├── arbor.yaml
    ├── collections/
    │   └── users/
    │       ├── collection.yaml
    │       ├── get.yaml
    │       └── create.yaml
    ├── environments/
    │   ├── local.yaml
    │   └── staging.yaml
    └── scenarios/
        └── user-lifecycle.yaml
```

Arbor recommends keeping project definitions under `.arbor/` to avoid conflicts with application directories. `.arbor/arbor.yaml` marks the workspace:

```yaml
version: 1
name: My API
defaultEnvironment: local

variables:
  api_version: v1

http:
  timeout: 30s
```

Workspace-level `variables` are shared across every environment. In the TUI, use `:vars` (also
`:variables` or `:variable`) to browse them, filter the list, describe a variable's workspace
scope, or press `e` to edit the authoritative `arbor.yaml` file.

A request is a versioned YAML file anywhere under `.arbor/collections/`:

```yaml
version: 1
kind: request
id: users.get
name: Get user
description: Fetch a single user by id.
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

The folder a request lives in is its **collection** (`.arbor/collections/users/get.yaml` → the
`users` collection). Add an optional `.arbor/collections/users/collection.yaml` marker to give a
collection a description. Every resource — workspace, request, collection, environment,
scenario — accepts an optional `description` field. Descriptions are never sent over the
wire; they exist so people *and* coding agents can understand a workspace without opening
every file.

See [Workspace format](docs/workspace-format.md) for the full schema and precedence rules.

## File uploads

A request can send `multipart/form-data` with `form` (text fields) and `files` (field → path):

```yaml
version: 1
kind: request
id: avatars.upload
name: Upload avatar
method: POST
url: "{{base_url}}/avatar"

form:
  caption: "Profile photo"

files:
  avatar: ./files/me.png
```

File paths are relative to the request file (absolute paths work too), and `{{variables}}`
resolve in both fields and paths. `form` alone (no files) is sent as
`application/x-www-form-urlencoded`. In the TUI, `:attach <field>=<path>` adds a file to the
selected request. See `examples/httpbin/collections/uploads` for a runnable upload.

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

## Workspaces

Each API project is its own workspace (its own `.arbor/arbor.yaml`, collections, and environments). In a monorepo, use one workspace per service unless the workspace intentionally models the whole API surface.
Arbor keeps a central registry — like k9s remembering your clusters — so you can hop between
projects without caring which directory you launched from. It lives at
`~/.config/arbor/config.yaml` on every platform (honoring `XDG_CONFIG_HOME`, or override with
`ARBOR_CONFIG`). Run `arbor config path` to print the exact location.

```text
~/apis/
├── github-apis/     ← a workspace
└── twilio-apis/     ← another workspace
```

The registry fills itself in: opening a workspace with `arbor` auto-registers it and records
it as most-recently-used. You can also add one explicitly.

```bash
arbor register ~/apis/twilio-apis   # add a workspace (defaults to cwd)
arbor workspaces                    # list registered workspaces (* marks last-used)
arbor unregister twilio-apis        # remove one
arbor -w github-apis                # open (or run against) a workspace by name, from anywhere
```

Running `arbor` from a directory that isn't a workspace reopens your last-used one. Inside the
TUI, `:ws` lists your workspaces and `Enter` switches; `:ws twilio-apis` switches directly.

This gives two levels of context, mirroring k9s: **`:ws`** switches the *project*
(github ↔ twilio), **`:use`/`:ctx`** switches the *environment* within a project
(staging ↔ prod). `-w` also works on `run`, `scenario`, `list`, and `describe`, so
`arbor run github.user -w github-apis` runs from any directory.

## Command line

```text
arbor                              Open the interactive workspace
arbor init                         Initialize a workspace
arbor register [dir]               Register a workspace (defaults to cwd)
arbor workspaces                   List registered workspaces
arbor unregister <name>            Remove a workspace from the registry
arbor config path                  Print the central config file location
arbor new request <ref>            Create a request
arbor new collection <name>        Create a collection
arbor new environment <name>       Create an environment
arbor new scenario <ref>           Create a scenario
arbor validate                     Validate all workspace files
arbor context --json               Print workspace context for coding agents
arbor schema <kind>                Print the JSON schema for a resource kind
arbor explain workspace-format     Print the workspace authoring guide
arbor skill install --agent all    Install the Arbor skill for all supported agents
arbor skill status                 Show installed Arbor agent skills
arbor skill init                   Add project-local Arbor agent instructions
arbor list requests                List request references
arbor list collections             List collections
arbor list environments            List environments
arbor list scenarios               List scenario references
arbor describe <ref>               Describe any resource (add --json for agents)
arbor run <ref>                    Run one request
arbor scenario <ref>               Run a scenario
arbor secret set <name>            Store a keychain secret
arbor secret delete <name>         Delete a keychain secret
```

Add `-w <name>` to target a registered workspace from anywhere (e.g. `arbor run github.user
-w github-apis`).

`arbor describe <ref> --json` and `arbor list <resource> --json` emit machine-readable
output — a discovery surface a coding agent can read to work in the workspace without
opening each file.

### Coding-agent skill

Arbor includes a reusable skill for Codex, Claude, and the shared agent skill directory. Install it for all supported locations with:

```bash
arbor skill install --agent all
```

Codex installation writes to both `~/.codex/skills/` and the shared `~/.agents/skills/` directory. Use `--agent agents` to install only the shared copy; Claude uses `~/.claude/skills/`.

Use `arbor skill status` to inspect the installation and `arbor skill update` after upgrading Arbor. `arbor skill init` creates a project-local `AGENTS.md`; if one already exists, it prints an Arbor section to add without overwriting repository instructions. The skill guides an agent to inspect an API codebase, create or update Arbor YAML resources, protect secrets, validate the workspace, and review the resulting Git diff. The YAML files remain the source of truth and can be edited by people, agents, or the CLI.

`arbor context --json` includes each resource's relative YAML `file` path so agents can update the correct definition without scanning the entire workspace.

Runtime variables can be supplied without editing files:

```bash
arbor run users.get --env staging --var user_id=123
```

Use `--json` with `arbor run` for machine-readable output.

## Commands inside the TUI

```text
:requests, :request, :req Open requests
:collections, :col        Open collections (Enter drills into a collection)
:scenarios, :scenario, :sc
                          Open scenarios
:variables, :variable, :vars
                          Open shared workspace variables
:environments, :env       Open environments
:workspaces, :ws          Switch workspace (Enter switches to the selected project)
:ws twilio-apis           Switch workspace directly
:use staging              Set the active environment
:ctx staging              Same as :use (k9s-style alias)
:attach doc=./file.pdf    Attach a multipart file to the selected request
:run users.get            Run a request by reference
:run auth.smoke           Run a scenario by reference
:aliases                  Show all resource aliases
:reload                   Reload files from disk
:help                     Open keyboard help
:quit                     Quit Arbor
```

Arbor also reloads the workspace after returning from `$EDITOR`. Use `Ctrl-r` after any other external edit.

### Workspace-local aliases

Arbor includes built-in aliases such as `:req`, `:sc`, `:vars`, and `:env`. Add project-specific aliases in `.arbor/aliases.yaml`; they are loaded when Arbor starts and on `Ctrl-r`:

```yaml
aliases:
  smoke: scenarios
  api: requests
  contexts: environments
```

Aliases intentionally target resource views, keeping command navigation as predictable as k9s while the workspace remains fully local and shareable.

Arbor follows k9s's global-plus-contextual convention for aliases and hotkeys. It loads user-wide files from `~/.config/arbor/` (honoring `XDG_CONFIG_HOME`) on every platform, then lets `.arbor/` files in the workspace override them.

```yaml
# .arbor/hotkeys.yaml
hotKeys:
  shift-0:
    shortCut: Shift-0
    description: Open requests
    command: requests
  shift-1:
    shortCut: Shift-1
    description: Open scenarios
    command: scenarios
```

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
