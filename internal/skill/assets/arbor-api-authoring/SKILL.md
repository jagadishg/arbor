---
name: arbor-api-authoring
description: Create and maintain Arbor API workspaces from an application codebase. Use when asked to generate, update, review, or validate Arbor collections, requests, environments, or scenarios.
---

# Arbor API authoring

Arbor is a Git-friendly API workspace: versioned YAML files define collections, requests, environments, and scenarios, and those files are the source of truth.

## Workflow

1. Determine the API boundary and read the repository's `AGENTS.md`. Use the repository root for a single-API repository. In a monorepo, use the service directory when that service independently owns and tests its API; otherwise use the monorepo root. Ask only when the boundary is genuinely ambiguous.
2. Search that boundary and its ancestors for an existing `.arbor/arbor.yaml` or `arbor.yaml`. Reuse it; never create a second workspace. If none exists, create `<api-boundary>/.arbor/` with `arbor init --name "Payments API" <api-boundary>`.
3. Inspect existing resources with `arbor context --json`. Use each resource's `file` field when updating it. Preserve stable IDs and existing conventions.
4. Inspect routes, handlers, OpenAPI documents, schemas, tests, and client code. Prefer actual behavior over assumptions.
5. Create or update the workspace root `arbor.yaml` for shared variables and settings, and YAML under the workspace's `collections/`, `environments/`, and `scenarios/` directories. Add descriptions explaining purpose, inputs, authentication, responses, and assumptions. Replace or remove the `example` scaffold once real resources exist.
6. Never write secret values. Use `env://NAME` or `keychain://service/account` references.
7. Run `arbor validate`, review the Git diff, and summarize the files and assumptions.
8. Execute requests only against safe and authorized environments; prefer local or designated test environments.

## Authoring rules

- Use `version: 1` and the correct `kind` in every resource file.
- Use explicit, stable request IDs such as `users.get` and `users.create`.
- Put requests in the collection directory that matches their domain.
- Reuse existing variables, headers, and authentication conventions before adding new ones.
- Keep values shared by every environment in `arbor.yaml` under `variables`; keep environment-specific values in the selected environment file.
- Use request-level assertions for invariant endpoint behavior and scenarios for workflows across requests.
- Do not invent response fields when the codebase does not establish them; document uncertainty instead.
- Do not change unrelated application files.
- Keep Arbor definitions in Git with the codebase; never save project definitions in Arbor's global configuration directory.
- Do not create multiple workspaces in a monorepo unless each API boundary is independently owned and tested.

Read `references/workspace-format.md` for field-level syntax and examples. Use `arbor explain workspace-format` or `arbor schema <kind>` when working with an installed Arbor binary.
