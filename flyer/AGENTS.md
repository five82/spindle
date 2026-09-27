# AGENTS.md

This file provides guidance when working with code in this repository.

## TL;DR

- Use `./deploy.sh` for deployments; do not reproduce its steps manually.
- Run `./check-ci.sh` before handing work back.
- Tests must not read real home directory or Spindle config - use `t.TempDir()` + `t.Setenv("HOME", ...)`.

## Project

Flyer is a **read-only TUI** for monitoring Spindle. Single-developer hobby project. The UI visual language follows the Monospace Design TUI standard as mapped in [docs/design.md](docs/design.md).

## Related Repos

| Repo | Path | Role |
|------|------|------|
| flyer | `~/projects/flyer/` | Read-only terminal UI for Spindle (this repo) |
| reel | `~/projects/reel/` | AV1 encoder embedded by Spindle; Flyer does not call it directly |
| shuttle | `~/projects/shuttle/` | Read-only native macOS monitor for Spindle |
| spindle | `~/projects/spindle/` | Daemon + CLI; Flyer polls its `[api].bind` endpoint |

GitHub: [flyer](https://github.com/five82/flyer) | [reel](https://github.com/five82/reel) | [shuttle](https://github.com/five82/shuttle) | [spindle](https://github.com/five82/spindle)

## Critical Expectations

**Architectural churn is embraced.** Optimize for clarity, not backwards compatibility.

- Apply YAGNI ("You Aren't Gonna Need It") and KISS ("Keep It Simple, Stupid"): build only what the current task requires; when two approaches work, take the simpler one.
- Do not just look for the easiest solution or fix. Find the best and most maintainable path forward.
- Prefer maintainable architecture and explicit logging over clever tricks.
- Break things forward. Remove deprecated paths; no compatibility shims.
- Simplification must not remove user-visible functionality. Eliminating a subprocess or code path that produces distinct output (log messages, CLI feedback, status indicators) is a behavior change, not a simplification.
- Coordinate major trade-offs with the user; never unilaterally defer functionality.
- Observability is key. We can not understand what is happening if we can not see it.
- When troubleshooting, gather evidence and test. Do not blindly guess.
- Keep edits ASCII unless the file already uses extended characters.

## Build, Test, Lint

```bash
go run ./cmd/flyer     # Run TUI
go test ./...          # Test
golangci-lint run      # Lint
./check-ci.sh          # Full CI (recommended before handoff)
```

## Scope Constraints

Flyer is intentionally limited:
- **Read-only**: No queue mutations, retries, or clears
- **Single operator**: Passes Spindle's bearer token but has no accounts or profiles

When considering features, ask: "Does this solve a real problem for daily use?" If not, skip it.
