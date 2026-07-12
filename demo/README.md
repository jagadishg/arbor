# Demo recordings

Reproducible asciinema recordings of Arbor's CLI and TUI, rendered to GIFs for
the README and the [project site](https://jagadishg.github.io/arbor/).

## Regenerate

```bash
bash demo/record.sh
```

This builds fresh binaries, starts a local mock server on `:8080`, records both
sessions at a fixed 110×30 terminal, and writes `cli.cast`/`cli.gif` and
`tui.cast`/`tui.gif` to `docs/demo/`. After recording, copy the assets the site
serves:

```bash
cp docs/demo/*.cast docs/demo/*.gif site/demo/
```

Requirements: [`asciinema`](https://asciinema.org), `expect`, and
[`agg`](https://github.com/asciinema/agg) (`brew install agg`).

## Files

- `mockserver/` — tiny fixed-response HTTP server so recordings need no network.
- `cli.sh` — the CLI walkthrough (typed out with a typewriter effect).
- `tui.exp` — drives the TUI via `expect`, recorded through asciinema.
- `lib.sh` — shared typing/narration helpers.
- `record.sh` — orchestrates everything.
