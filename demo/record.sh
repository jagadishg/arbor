#!/usr/bin/env bash
# Records Arbor's CLI and TUI demos and renders them to GIFs. Reproducible:
# builds fresh binaries, runs a local mock server, records with asciinema at a
# fixed terminal size, and converts with agg. Requires asciinema, expect, agg.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$PWD"
DEMO="$ROOT/demo"
OUT="$ROOT/docs/demo"
mkdir -p "$OUT"

for tool in asciinema expect agg; do
  command -v "$tool" >/dev/null || { echo "missing required tool: $tool" >&2; exit 1; }
done

echo "==> building binaries"
go build -o "$ROOT/bin/arbor" ./cmd/arbor
go build -o "$ROOT/bin/mockserver" ./demo/mockserver
export PATH="$ROOT/bin:$PATH"

echo "==> starting mock server on :8080"
mockserver :8080 &
MOCK=$!
trap 'kill "$MOCK" 2>/dev/null || true; rm -rf "$WORK"' EXIT
sleep 1

# Shared workspace for the TUI demo (mirrors the CLI walkthrough).
WORK="$(mktemp -d)"
arbor init "$WORK/myapi" -n myapi >/dev/null
( cd "$WORK/myapi"
  arbor new collection users -d "User endpoints" >/dev/null
  arbor new request users.get -m GET -u '{{base_url}}/users/1' -n "Get user" >/dev/null )

AGG_OPTS=(--font-size 16 --theme asciinema --idle-time-limit 2 --last-frame-duration 3)

echo "==> recording CLI demo"
expect -c "set stty_init \"rows 30 cols 110\"; spawn asciinema rec $OUT/cli.cast --overwrite -f asciicast-v2 -c \"bash $DEMO/cli.sh\"; set timeout -1; expect eof"
agg "${AGG_OPTS[@]}" "$OUT/cli.cast" "$OUT/cli.gif"

echo "==> recording TUI demo"
expect "$DEMO/tui.exp" "$OUT/tui.cast" "$WORK/myapi"
agg "${AGG_OPTS[@]}" "$OUT/tui.cast" "$OUT/tui.gif"

echo "==> done"
ls -la "$OUT"
