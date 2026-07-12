# Arbor agent instructions

This repository contains an Arbor API workspace. When creating or updating Arbor resources:

- Use the repository root for a single API. In a monorepo, use the independently owned service directory when appropriate; otherwise use the monorepo root.
- Inspect the existing workspace with `arbor context --json` before editing.
- Read the API routes, handlers, OpenAPI documents, and tests before inventing requests.
- Keep request IDs stable and place requests in the matching collection directory.
- Never write resolved secret values; use `env://NAME` or `keychain://service/account`.
- Run `arbor validate` and review the Git diff after changes.
- Execute requests only against safe and authorized environments.

The Arbor agent skill provides the complete authoring workflow. Project-specific instructions may be added below this section.
