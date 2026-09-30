# flyer

A read only terminal dashboard for [Spindle](https://github.com/five82/spindle),
the disc-ripping daemon. It polls the Spindle API and shows queue status, item
details, and logs.

## Expectations

Flyer is a personal tool. I'm sharing it because I believe in open source but
I'm not a maintainer. Expect rough edges.

## Features

- **Dashboard.** Queue table with completed-file counts and filtering, plus a
  live NOW band that names the item and task holding each scheduler resource
  (drive, GPU, encode).
- **Drive availability.** The header always shows whether the optical drive is
  AVAILABLE, BUSY, or PAUSED.
- **Item inspector.** Full-screen drill-in for one item, with Overview, Episodes
  (File for movies), Problems, Logs, and Events tabs. Independent work and
  explicit waits remain visible. Long operations disclose measured work after
  ten seconds; unknown totals never become a percentage. Any ETA is approximate
  and scoped to the current file's video, not the whole item. On the Episodes
  tab, `t` toggles secondary file evidence without hiding the inventory. Output
  checks describe Apply's final files, separately from Reel's intermediate
  checks. Events opens on the newest transitions, folds each operation's start
  and end into one row with its duration, and retains history across daemon
  restarts, until the transient queue is cleared.
- **Problems triage.** Current failures, review needs, unavailable checks, and
  nonfatal outcomes such as skipped subtitles, one keypress from details.
  Bounded diagnostic history is separate from current structured issues.
- **Logs.** Daemon and per-item logs with highlighting, follow mode, and filters
  for level, stage, asset, task, and attempt. Each event is one row with its
  decision outcome inline; `t` expands every structured field. Failed fetches
  retain their error and mark retained data stale rather than empty.
- **Search.** Regex log search with `n`/`N`. The queue's `/` filters rows by
  title.
- **Themes.** Slate and Nightfox, cycled with `T`.

## Install

```bash
go install github.com/five82/spindle/flyer/cmd/flyer@latest
```

Requirements:

- Go 1.27.1+
- A running Spindle daemon with `[api].bind` configured
- Local mode: read access to Spindle's config and state directory
  (`~/.local/state/spindle` by default), which holds the daemon log
- Remote mode: an API endpoint and bearer token (see
  [Remote Access](#remote-access))

Flyer runs anywhere it can reach the Spindle API. To build from a source
checkout instead:

```bash
git clone https://github.com/five82/spindle.git
cd spindle/flyer && go build ./cmd/flyer
```

## Usage

```bash
flyer
```

| Flag       | Default                                | Purpose                            |
| ---------- | -------------------------------------- | ---------------------------------- |
| `--config` | `$XDG_CONFIG_HOME/spindle/config.toml` | Spindle config file to read        |
| `--poll`   | `2`                                    | Refresh interval in seconds        |
| `--api`    | none                                   | Spindle API endpoint (remote mode) |
| `--token`  | none                                   | API bearer token (remote mode)     |

Press `h` or `?` in the TUI for keyboard shortcuts.

## Remote Access

Flyer reads Spindle's local config by default. Point it at a remote daemon with
flags or environment variables:

| Setting  | Flag      | Environment variable |
| -------- | --------- | -------------------- |
| Endpoint | `--api`   | `FLYER_API_ENDPOINT` |
| Token    | `--token` | `FLYER_API_TOKEN`    |

```bash
flyer --api http://server:7487 --token mysecrettoken
```

Flags take precedence, then environment variables, then the local config.
Spindle does not listen on TCP by default, so a local setup must enable it:

```toml
[api]
bind = "127.0.0.1:7487"
token = "choose-a-token"
```

See the [Spindle operator guide](https://github.com/five82/spindle#configure)
for server setup.

## Development

From the monorepo root:

```bash
go run ./flyer/cmd/flyer  # run without installing
go test ./flyer/...       # Flyer-only tests; no encoder libraries needed
./check-ci.sh             # full monorepo CI
./deploy.sh flyer         # build this checkout over the installed binary
```

GitHub is the public mirror; development and CI run on a private Forgejo
instance. See the [root guide](../README.md#development-checks) for native
dependencies required by full monorepo checks.

The deploy script keeps the previous binary beside the installed one and
verifies the installed copy.

See [AGENTS.md](AGENTS.md) for project structure and workflow. The UI visual
language and theme palettes live in [docs/design.md](docs/design.md) and
[docs/themes.md](docs/themes.md).

## License

[GPL-3.0](LICENSE)
