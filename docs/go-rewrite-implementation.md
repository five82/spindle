# Spindle Go Rewrite Implementation Plan

This document describes the step-by-step plan for rebuilding Spindle in Go, following the goals captured in `docs/go-rewrite-spec.md`. It is written for Codex CLI contributors working in this repository. The plan assumes the current Python code remains authoritative until the Go stack reaches feature parity and passes full validation.

## Guiding Principles
- Preserve all end-user functionality and queue semantics while simplifying internals as described in the spec.
- Implement clean package boundaries that mirror the workflow stages and external integrations.
- Prefer incremental, verifiable steps; maintain a runnable codebase throughout the migration.
- Rely on Git history instead of retaining legacy Python code once the Go implementation is complete.

## Tooling & Environment
- Install Go ≥ 1.22 locally (`go version` should confirm). No global Go modules should be required.
- Continue using `uv` tooling for Python until the cutover to keep CI and support scripts operational.
- Introduce Go lint/test tooling:
  - `go test ./...` for unit/integration tests.
  - `golangci-lint` for static checks (add to repo as part of implementation).
- Update `check-ci.sh` near the end of the project to execute Go tests/lints instead of Python checks.

## High-Level Phases
1. **Foundation setup** – create Go module, directory structure, shared libraries, configuration, and logging.
2. **Workflow core** – implement queue persistence, state machine, daemon supervision, and CLI skeleton.
3. **Stage handlers** – port identification, ripping, encoding, organizing stages with external integrations.
4. **Supporting services** – rebuild notification, plex client, and ancillary utilities.
5. **Observability & reliability** – structured logs, metrics, health endpoints (as scoped in the spec).
6. **Parity validation** – end-to-end tests, CLI behavior checks, side-by-side verification against Python behavior.
7. **Cutover & cleanup** – update docs/scripts, flip default entry points, remove Python code, finalize CI.

Each phase should land as a cohesive set of commits/PRs. Avoid starting the next phase before the current one is functional and tested.

## Phase 1: Foundation Setup
1. Create Go module at repository root (`go mod init spindle`) and check in go.mod/go.sum.
2. Lay out the directories defined in the spec (`/cmd`, `/internal/...`, `/pkg`). Ensure packages compile even with stub implementations.
3. Implement `internal/config`:
   - Load `~/.config/spindle/config.toml` using Go TOML parser.
   - Provide embedded defaults for optional values so the daemon can start without manual edits.
   - Include validation mirroring `src/spindle/config.py` semantics.
4. Add logging bootstrap in `internal/logging` (zap or zerolog) with options for JSON/plain output.
5. Introduce shared error types and context helpers under `internal/services` for later use.

Deliverables: compiling Go skeleton with configuration loading, basic logger, and unit tests covering default behavior.

## Phase 2: Workflow Core
1. Implement SQLite persistence under `internal/queue`:
   - Define models matching the Python `QueueItem` and `QueueItemStatus` fields.
   - Provide schema migrations (embedded SQL) and auto-healing logic similar to `QueueManager._ensure_schema` but leveraging versioned migrations.
   - Implement repository functions (create, update, find-by-fingerprint, reset-stuck, list/status queries).
2. Build `internal/workflow` state machine:
   - Create status enum and `Stage` interface (`Prepare`, `Execute`, optional `Rollback`).
   - Manage transitions atomically via queue repository helpers.
3. Implement daemon supervision in `internal/daemon`:
   - Single-instance guard (similar to `process_manager.py`).
   - Signal handling and graceful shutdown.
4. Create daemon entry point (`cmd/spindled/main.go`) and CLI client skeleton (`cmd/spindle/main.go`). Use Cobra to mirror existing commands with placeholder handlers invoking RPC stubs.
5. Establish IPC layer (Unix domain socket JSON-RPC or protobuf). Implement simple ping/status endpoints for now.

Deliverables: Go daemon starts, creates queue database, handles `spindle start|stop|status`, and responds with stub data. Unit tests cover queue migrations and basic workflow transitions.

## Phase 3: Stage Handlers & Integrations
Work through each stage sequentially, ensuring the daemon runs end-to-end at each checkpoint.

### Identification Stage (`PENDING → IDENTIFYING → IDENTIFIED`/`REVIEW`)
- Map Python logic from `components/disc_handler.py`, `services/tmdb.py`, and supporting modules.
- Implement MakeMKV scanning wrapper in `internal/disc` with fingerprint deduplication and error handling.
- Port TMDB client with caching and rate limiting (`internal/identification`).
- Add heuristics for classification and metadata storage.
- Provide tests with mocked MakeMKV/TMDB.

### Ripping Stage (`IDENTIFIED → RIPPING → RIPPED`)
- Implement MakeMKV ripping worker; stream progress into queue updates and notifications.
- Ensure automatic eject executes on success.
- Handle failure modes with typed errors that map to `FAILED`.

### Encoding Stage (`RIPPED → ENCODING → ENCODED`)
- Create Drapto client (long-lived worker) under `internal/encoding`.
- Stream JSON progress to queue; respect cancellation and retries.
- No encoder plug-ins—Drapto remains the only supported backend.

### Organizing Stage (`ENCODED → ORGANIZING → COMPLETED`)
- Port file-move and Plex-trigger logic from `components/organizer.py` and `services/plex.py`.
- Ensure library paths and naming mirror current behavior.
- Implement manual-file ingestion path that starts items at the `RIPPED` stage.

At the end of Phase 3, the Go daemon should process discs end-to-end using live integrations.

## Phase 4: Supporting Services
- Rebuild `internal/notifications` for ntfy messages, reusing templates from Python.
- Implement remaining CLI commands (queue listing, retry, clear, add-file, show).
- Add structured log tailing endpoint for `spindle show --follow` (stream logs via IPC or file tail helper).
- Introduce any auxiliary maintenance commands required by the Python CLI parity.

## Phase 5: Observability & Reliability
- Finalize structured logging fields (queue item ID, stage, correlation IDs).
- Optional metrics endpoint (Prometheus) as scoped in the spec; can be disabled in config.
- Add self-health checks per stage to allow future aggregation (no combined API yet per decision).
- Harden error handling: guarantee every failure records actionable context and drives correct queue status.

## Phase 6: Testing & Validation
1. Unit tests for each package with Go’s testing framework.
2. Integration tests spinning up in-memory/temporary SQLite DB and fake external clients.
3. CLI contract tests verifying output parity with Python commands (focus on `status`, `queue list`, `queue retry`, `show`).
4. End-to-end scenario: mock MakeMKV/Drapto/TMDB/Plex and run full workflow.
5. Update `check-ci.sh` to execute Go tests and linters; keep Python tooling temporarily if needed for legacy branches until cutover.
6. Manual smoke tests on actual hardware if available (document steps and results).

## Phase 7: Cutover & Cleanup
1. Prepare migration instructions for users (even if only the maintainer):
   - Stop Python daemon.
   - Backup existing `queue.db` and log files.
   - Run migration tool (if schema changed) to upgrade database for Go.
2. Swap CLI tooling:
   - `cmd/spindle` becomes default entry point.
   - Update installation instructions (README) to point to Go binary distribution or `go install` path.
3. Update automation:
   - Rewrite `.github/workflows/ci.yml` to use the Go toolchain (`go test`, `golangci-lint`) once Python code is removed.
   - Adjust `.github/dependabot.yml` to manage Go modules (or disable if not needed).
4. Remove Python implementation:
   - Delete `src/`, `tests/`, `pyproject.toml`, `uv.lock`, and other Python-specific files once Go version is validated.
   - Clean up scripts that assume Python (`scripts/install-user-service.sh` may need editing for Go binary path).
   - Ensure Git history retains Python code for archival reference.
5. Replace references in `check-ci.sh` to run Go tasks only.
6. Update documentation:
   - `README.md` – installation, requirements, CLI usage, workflow description for Go daemon.
   - `docs/` – review existing guides (`workflow.md`, `development.md`, `content-identification.md`) and revise where behavior or internals changed.
   - `AGENTS.md` – adjust contributor guidance to describe Go tooling, testing expectations, and new architecture map.
   - `docs/go-rewrite-spec.md` – mark as delivered or archive if no longer needed.
7. Tag release or create changelog entry summarizing the rewrite.

## Mapping Reference
| Python Source | Go Destination | Notes |
| --- | --- | --- |
| `src/spindle/core/daemon.py` | `internal/daemon`, `cmd/spindled` | Signal handling, single-instance enforcement |
| `src/spindle/core/orchestrator.py` | `internal/workflow` | Stage orchestration, queue polling |
| `src/spindle/components/disc_handler.py` | `internal/disc`, `internal/identification` | Disc detection, scanning, metadata |
| `src/spindle/components/encoder.py` | `internal/encoding` | Drapto worker |
| `src/spindle/components/organizer.py` | `internal/organizer` | File moves, Plex updates |
| `src/spindle/services/*.py` | `internal/*` packages | Each service gets a typed Go client |
| `src/spindle/storage/queue.py` | `internal/queue` | Models, migrations, repositories |
| CLI (`src/spindle/cli.py`) | `cmd/spindle` | Cobra commands w/ RPC calls |
| `check-ci.sh` | `check-ci.sh` (Go version) | Update to run Go tooling |

## Documentation & Process Updates
- Maintain `docs/go-rewrite-spec.md` during the project; update if the scope evolves.
- Track progress in the repo (issues/PRs) referencing phases above.
- For each phase landing, update relevant docs immediately (avoid deferring documentation debt).
- After removal of Python code, audit the repository for stale references (`rg 'python'` etc.).

## Risk & Mitigation
- **Regression risk**: rely on integration tests and staged rollouts (side-by-side runs) before removal.
- **External dependency drift**: pin MakeMKV/Drapto versions in documentation and provide compatibility checks.
- **Operational change**: update service install scripts and README instructions early in Phase 7 to avoid confusion during cutover.

## Completion Criteria
- Go daemon fully replaces Python implementation with proven parity across all workflows.
- CI green on Go tests/lints; `check-ci.sh` reflects new toolchain.
- Documentation (README, AGENTS, docs/*) updated to describe Go architecture and contributor expectations.
- Python code removed from main branch with history preserved in Git.
