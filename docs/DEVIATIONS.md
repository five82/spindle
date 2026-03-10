# Design Document Deviations

Deviations from the 12 design specification documents, with reasoning.

---

## CLI Commands (API_INTERFACES.md)

### Disc commands use placeholder HTTP calls

`spindle disc pause`, `spindle disc resume`, and `spindle disc detect` print
confirmation messages but don't yet make HTTP calls to the daemon API. These
require a running daemon to test.

---

## Daemon Control (DESIGN_DAEMON.md)

### daemonctl.Start does not fork a background process

`daemonctl.Start()` returns an instruction to use `spindle daemon` rather than
forking a child process. This avoids the complexity of cross-platform process
management. The expected deployment model is systemd or direct invocation.

### daemonctl.Stop sends instruction rather than signal

`daemonctl.Stop()` does not send SIGTERM to the daemon PID. It returns an
instruction message. In practice, the daemon is stopped via the HTTP API
(`POST /api/daemon/stop`) or SIGTERM from systemd/shell.

---

## Configuration (DESIGN_OVERVIEW.md, DESIGN_INFRASTRUCTURE.md)

### DaemonLogPath auto-derived

Added `Config.DaemonLogPath()` returning `{state_dir}/daemon.log`. This was
implied by the design (daemon logs to a file) but not explicitly specified as
a config method.

---

## Rip Cache and Disc ID Cache

### Added List/Remove/Clear methods

`ripcache.Store` and `discidcache.Store` gained `List()`, `Remove()`, and
`Clear()` methods to support the CLI commands specified in API_INTERFACES.md
sections 1.8 and 1.11. The design docs specified the CLI commands but didn't
enumerate the required store methods.

---

## HTTP API (API_INTERFACES.md Section 2)

### OpenWithFallback health check

`queueaccess.OpenWithFallback` checks `GET /api/health` to detect daemon
availability. The design doc doesn't specify a `/api/health` endpoint, but it's
needed for the HTTP-or-direct-DB fallback logic.
