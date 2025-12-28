# Spindle CLI Reference

The `spindle` binary runs both the daemon and the CLI. This reference groups
the most common commands; see `spindle --help` for the full tree.

## Daemon Lifecycle

```bash
spindle start          # launch the daemon (background)
spindle status         # quick health summary
spindle stop           # graceful shutdown
spindle show --follow  # tail logs with color
spindle show --lines 50 --follow  # snapshot + follow
```

## Configuration & Validation

```bash
spindle config init       # scaffold ~/.config/spindle/config.toml
spindle config validate   # check paths, credentials, syntax
```

## Queue Management

```bash
spindle queue list                  # every item with status + fingerprint
spindle queue list --status encoding --status failed
spindle queue status                # counts by lifecycle state
spindle queue health                # condensed diagnostics
spindle queue show <id>             # detailed view with per-episode map
spindle queue clear --completed     # drop completed entries
spindle queue clear-failed          # remove failed entries
spindle queue reset-stuck           # kick stalled items forward
spindle queue retry <id...>         # retry one or more items
```

## Utilities

```bash
spindle gensubtitle /path/to/video.mkv  # regenerate subtitles (add --forceai)
spindle cache stats                     # inspect rip cache usage
spindle cache prune                     # force cache cleanup
spindle test-notify                     # send a test ntfy message
```

## Troubleshooting Shortcuts

- `spindle identify /dev/sr0 --verbose` — run TMDB identification without
  touching the queue.
- `spindle show --follow | grep -i error` — tail logs with a filter.
- `sqlite3 ~/.local/share/spindle/queue.db 'SELECT id, status FROM queue_items;'`
  — inspect the queue directly (daemon can be stopped).

For deeper context on how these commands interact with the workflow, read
`docs/workflow.md`. HTTP integrations live in `docs/api.md`.
