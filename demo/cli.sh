#!/usr/bin/env bash
# CLI walkthrough for Arbor's demo: create a workspace, add a collection and a
# request, explore them, and run one — all from the command line. Expects `arbor`
# on PATH and a mock server listening on http://localhost:8080 (see record.sh).
set -euo pipefail
cd "$(dirname "$0")"
source ./lib.sh

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

sleep 0.8
title 'Arbor — the API client that lives in your terminal'

comment 'Create a new workspace'
type_run 'arbor init myapi'
type_run 'cd myapi'

comment 'Add a collection and a request (readable YAML, Git-friendly)'
type_run "arbor new collection users -d 'User endpoints'"
type_run "arbor new request users.get -m GET -u '{{base_url}}/users/1' -n 'Get user'"

comment 'Explore the workspace'
type_run 'arbor list requests'
type_run 'arbor describe users.get'

comment 'Run it — variables resolve and the response comes back'
type_run 'arbor run users.get' 2.0

comment 'Same definitions power the interactive TUI — just run: arbor'
sleep 2.2
