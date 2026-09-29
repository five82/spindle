# Flyer

Common repository rules live in [../AGENTS.md](../AGENTS.md).

Flyer is a read-only TUI for monitoring Spindle. The UI visual language follows
[docs/design.md](docs/design.md); palettes live in
[docs/themes.md](docs/themes.md).

## Boundaries

- Read-only: no queue mutations, retries, or clears.
- Single operator: passes Spindle's bearer token, with no accounts or profiles.
- Poll Spindle's `[api].bind` endpoint; do not import daemon or Reel packages.
- Keep Flyer buildable without CGO or native encoding libraries.
- Tests must not read the real home directory or Spindle config; use
  `t.TempDir()` and `t.Setenv("HOME", ...)`.
- Simplification must preserve log messages, CLI feedback, and status
  indicators.

## Development

From the monorepo root:

```bash
go run ./flyer/cmd/flyer
go test ./flyer/...
./deploy.sh flyer  # when deploying
```

When considering features, ask whether they solve a real daily-use problem; do
not add speculative abstractions.
