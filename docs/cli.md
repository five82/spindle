# Spindle CLI Reference

The `spindle` binary runs both the daemon and the CLI. This reference groups
the most common commands; see `spindle --help` for the full tree.

Global flags:
- `--log-level` (debug|info|warn|error) sets CLI logging verbosity for any command.
- `-v`/`--verbose` is shorthand for `--log-level=debug`.
- `-c`/`--config` specifies a custom configuration file path.

## Daemon Lifecycle

```bash
spindle start               # launch the daemon (background)
spindle stop                # graceful shutdown (terminates process)
spindle restart             # stop then start the daemon
spindle status              # system status, dependencies, and queue summary
spindle show --follow       # tail logs with ANSI color formatting
spindle show --lines 50 --follow  # snapshot + follow
```

The `start` and `restart` commands accept a `--diagnostic` flag to enable
separate DEBUG-level logs for troubleshooting.

## Disc Detection Control

```bash
spindle disc pause          # pause detection of new disc insertions
spindle disc resume         # resume disc detection
```

Pausing disc detection stops the daemon from queueing new discs while allowing
already-queued items to continue processing. State resets on daemon restart.

## Configuration & Validation

```bash
spindle config init         # scaffold ~/.config/spindle/config.toml
spindle config validate     # check paths, credentials, syntax
```

## Queue Management

```bash
spindle queue status                # counts by lifecycle state
spindle queue list                  # every item with status + fingerprint
spindle queue list --status failed  # filter by status (repeatable)
spindle queue show <id>             # detailed view with per-episode map
spindle queue health                # condensed diagnostics

# Removing items
spindle queue clear <id>            # remove specific item by ID
spindle queue clear 10 11 12        # remove multiple items
spindle queue clear --all           # remove all items
spindle queue clear --completed     # remove only completed items
spindle queue clear --failed        # remove only failed items
spindle queue clear-failed          # shorthand for --failed

# Recovery
spindle queue retry                 # retry all failed items
spindle queue retry <id>            # retry specific failed item
spindle queue retry 123 --episode s01e05  # retry single episode
spindle queue reset-stuck           # return in-flight items to start of stage
spindle queue stop <id>             # halt processing for specific items
```

## Cache Management

The rip cache stores MakeMKV output for reuse. Requires `rip_cache.enabled = true`.

```bash
spindle cache stats                 # show all cached entries with sizes
spindle cache rip                   # rip a disc directly into the cache
spindle cache process <number>      # queue a cached entry for processing
spindle cache remove <number>       # remove entry by number (from stats)
spindle cache clear                 # remove all cached entries
spindle cache crop <file>           # run crop detection (troubleshooting)
spindle cache commentary <file>     # run commentary detection (troubleshooting)
```

## Staging Management

```bash
spindle staging list                # list staging directories with sizes
spindle staging clean               # remove orphaned staging directories
spindle staging clean --all         # remove all staging directories
```

## Utilities

```bash
spindle identify [device]           # identify a disc without queueing
spindle gensubtitle <file>          # generate subtitles for encoded file
spindle gensubtitle <file> --forceai        # force WhisperX, skip OpenSubtitles
spindle gensubtitle <file> --fetch-forced   # also fetch forced subtitles
spindle preset-decider <title>      # test LLM preset selection
spindle test-notify                 # send a test ntfy message
```

## Troubleshooting Shortcuts

- `spindle --log-level=debug identify /dev/sr0` - run TMDB identification without
  touching the queue.
- `spindle show --follow | grep -i error` - tail logs with a filter.
- `sqlite3 ~/.local/share/spindle/logs/queue.db 'SELECT id, status FROM queue_items;'`
  - inspect the queue directly (daemon can be stopped).

For deeper context on how these commands interact with the workflow, read
`docs/workflow.md`. HTTP integrations live in `docs/api.md`.
