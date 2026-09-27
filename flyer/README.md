# flyer

A read only terminal dashboard for [Spindle](https://github.com/five82/spindle), the disc-ripping daemon. It polls the Spindle API and shows queue status, item details, and logs.

## Expectations

Flyer is a personal tool. I'm sharing it because I believe in open source but I'm not a maintainer. Pull requests are welcome when they fit the project's goals. Expect rough edges.

## Features

- **Dashboard.** Queue table with progress and filtering, plus a live NOW band
  that names the item and task holding each scheduler resource (drive, GPU,
  encode).
- **Drive availability.** The header always shows whether the optical drive is
  AVAILABLE, BUSY, or PAUSED.
- **Item inspector.** Full-screen drill-in for one item, with Overview,
  Episodes, Problems, and Logs tabs. Episodes applies to TV box sets.
- **Problems triage.** Every failed or review item with its lead reason, one
  keypress away from the details.
- **Logs.** Daemon and per-item logs with highlighting, follow mode, and filters
  for level, component, lane, and request.
- **Search.** Regex log search with `n`/`N`. The queue's `/` filters rows by
  title.
- **Themes.** Slate and Nightfox, cycled with `T`.

## Install

```bash
go install github.com/five82/flyer/cmd/flyer@latest
```

Requirements:

- Go 1.27.1+
- A running Spindle daemon with `[api].bind` configured
- Local mode: read access to Spindle's config and state directory
  (`~/.local/state/spindle` by default), which holds the daemon log
- Remote mode: an API endpoint and bearer token (see
  [Remote Access](#remote-access))

Flyer runs anywhere it can reach the Spindle API. To build from a source checkout instead:

```bash
git clone https://github.com/five82/flyer.git
cd flyer && go build ./cmd/flyer
```

## Usage

```bash
flyer
```

| Flag | Default | Purpose |
|------|---------|---------|
| `--config` | `$XDG_CONFIG_HOME/spindle/config.toml` | Spindle config file to read |
| `--poll` | `2` | Refresh interval in seconds |
| `--api` | none | Spindle API endpoint (remote mode) |
| `--token` | none | API bearer token (remote mode) |

Press `h` or `?` in the TUI for keyboard shortcuts.

## Remote Access

Flyer reads Spindle's local config by default. Point it at a remote daemon with flags or environment variables:

| Setting | Flag | Environment variable |
|---------|------|----------------------|
| Endpoint | `--api` | `FLYER_API_ENDPOINT` |
| Token | `--token` | `FLYER_API_TOKEN` |

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

```bash
go run ./cmd/flyer     # run without installing
go test ./...          # run tests
./check-ci.sh          # full local CI: tests, race, lint, govulncheck
./deploy.sh            # build this checkout over the installed binary
```

The deploy script keeps the previous binary beside the installed one and
verifies the installed copy.

See [AGENTS.md](AGENTS.md) for project structure and workflow. The UI visual
language and theme palettes live in [docs/design.md](docs/design.md) and
[docs/themes.md](docs/themes.md).

## License

[GPL-3.0](LICENSE)
