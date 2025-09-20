# Spindle Go Rewrite Specification

## Background & Motivation
Spindle's current Python codebase has grown quickly to orchestrate disc detection, ripping, encoding, organization, and notifications. A ground-up rewrite in Go aims to keep the product feature-complete while improving long-term maintainability, deployment simplicity, and observability. Go's static typing, built-in concurrency primitives, and single-binary distribution align with Spindle's daemon-first workflow and reduce the need for a managed Python environment.

## Goals & Non-Goals
- Maintain all existing end-user functionality, queue states, and CLI commands unless changed in a future scoped discussion.
- Preserve the daemon-only execution model: the CLI communicates with a long-running background process responsible for the workflow lifecycle.
- Embrace opportunities to simplify overengineered Python patterns (ad-hoc threading, implicit shared state, duplicated queue logic) without removing supported features.
- Improve separation of duties with package boundaries that align with the workflow phases and external integrations.
- Non-goal: designing new product features or changing the lifecycle stages (`PENDING → ... → COMPLETED`, including `FAILED` and `REVIEW`).
- Non-goal: implementing a compatibility shim for legacy Python components.
- Non-goal: introducing a generic encoder plugin system; Drapto remains the sole supported encoder in this rewrite.

## Functional Requirements (Parity Targets)
- Queue lifecycle: identical status progression, retry semantics, stuck-item recovery, and persisted progress metadata.
- Optical disc monitoring: device detection, fingerprint deduplication, and automatic eject on successful `RIPPED` transition.
- External integrations: MakeMKV, Drapto, TMDB, Plex, and ntfy must retain current capabilities and configuration options.
- Manual file ingestion (`add-file`) that skips optical stages but joins encoding/organizing flows.
- Configuration management via `~/.config/spindle/config.toml` with embedded sane defaults so fresh installs can run immediately; environment overrides are explicitly deferred for now.
- CLI surface: `spindle start|stop|status|show`, queue commands (`list`, `status`, `retry`, `clear`, `health`), diagnostic helpers, and notification test.
- Observability: structured logging, progress reporting, and user-facing notifications at the same lifecycle checkpoints.

## Architectural Overview
Proposed top-level layout:
```
/cmd
  /spindle        # CLI client (talks to daemon via RPC/IPC)
  /spindled       # Daemon entry-point (systemd-friendly)
/internal
  /config         # Typed configuration loading & validation
  /daemon         # Lifecycle supervision, single-instance enforcement, signal handling
  /workflow       # Pipeline coordinator, stage schedulers, status transitions
  /queue          # Persistence layer (SQLite), migrations, repositories, models
  /disc           # Optical device monitor, MakeMKV scanning & fingerprinting
  /encoding       # Drapto process management and progress streaming
  /organizer      # Library move, Plex refresh, library tree helpers
  /identification # TMDB client, heuristics, metadata enrichment
  /notifications  # ntfy client, templating
  /services       # Shared HTTP utilities, retry helpers, rate limiting
  /logging        # Structured logging setup, log rotation, metrics hooks
/pkg             # Optional shared utilities intended for external reuse (keep minimal)
```
Key principles:
- Packages expose clear interfaces; higher layers depend on abstractions, not concrete implementations.
- `internal/workflow` orchestrates stage handlers through a typed state machine rather than ad-hoc threading.
- CLI packages interact with the daemon via a defined RPC layer (e.g., Unix domain sockets + protobuf/JSON-RPC) instead of process-global state.

## Workflow Coordination
- Implement a typed state machine that maps each queue item status to a handler implementing a `Stage` interface (`Prepare`, `Execute`, `Rollback` optional).
- Use worker pools for long-running activities (ripping, encoding) with context cancellation, ensuring daemon shutdown waits on in-flight tasks.
- Implement deduplication logic during `PENDING` handling using deterministic fingerprints and queue lookups.
- Codify retry/backoff policies within the stage handlers to avoid scattering queue writes across packages.

## Queue & Persistence
- Keep SQLite as the source of truth for queue items; leverage Go migrations (e.g., `pressly/goose` or embedded SQL migrations with `atlas`) to version schemas.
- Define data models in `internal/queue/models.go` with explicit types for statuses, progress, and metadata blobs.
- Provide transactional helpers to ensure multi-step updates (status change + progress + notifications) are atomic.
- Evaluate simplifying the Python-era reset paths by persisting heartbeat timestamps and letting the daemon reclaim stale `PROCESSING` rows without bespoke code in multiple packages.

## External Integrations
- **MakeMKV**: Wrap CLI invocations with context-aware command runners, streaming logs to both the notifier and stage progress channel.
- **Drapto**: Implement a long-lived worker that manages the JSON progress stream and reports to the queue via structured events; remove duplicated polling loops. Drapto is a first-class dependency—no encoder plugin architecture is planned.
- **TMDB**: Centralize rate limiting and caching in `internal/identification/tmdb`; share between identification and organizer steps when metadata is needed twice.
- **Plex**: Provide a minimal client with typed responses, retry on HTTP 5xx, and cancellation-aware requests.
- **ntfy**: Encapsulate message formatting; stage handlers emit well-defined events consumed by the notifier.

## CLI & Daemon Interaction
- Use Go interfaces (`DaemonClient`) to abstract IPC. Start with Unix domain sockets for local use, leaving room for future gRPC if remote control is needed.
- Mirror existing commands with `cobra`-based CLI to maintain discoverability and auto-generated help.
- Provide continuous log tailing (`spindle show --follow`) by streaming daemon log output through the IPC layer or reading directly from a structured log sink (e.g., zap + file tailing API).

## Observability & Reliability
- Adopt structured logging (zap or zerolog) with fields for queue item ID, stage, and correlation IDs.
- Add lightweight metrics (Prometheus format) exposed via optional HTTP listener for systemd health checks.
- Standardize error surfaces: each stage handler returns typed errors that map to `FAILED` or `REVIEW` outcomes with user-facing guidance.

## Overengineering Reduction Opportunities
- Replace ad-hoc thread spawning (`threading.Thread` per disc/scan) with coordinated worker pools governed by contexts; simplifies shutdown and reduces shared mutable state.
- Collapse duplicate queue mutation logic across components into a single repository layer with explicit methods (e.g., `TransitionTo(status, opts)`), eliminating manual field juggling.
- Remove placeholder/stub modules (`WorkflowManager` stubs) by reifying workflow coordination as a first-class package with well-defined responsibilities.
- Avoid implicit dependency injection via setters (e.g., `set_queue_manager`) in favor of constructor injection to make dependencies explicit and testable.
- Use Go interfaces to mock external services in tests rather than monkey-patching modules, reducing the need for custom test harnesses.

## Testing Strategy
- Mirror the current integration-heavy approach with Go test suites per stage package.
- Provide end-to-end tests that spin up an in-memory queue, fake service clients, and assert full lifecycle transitions.
- Add contract tests for CLI ↔ daemon IPC to ensure command outputs match legacy expectations.
- Maintain fixture parity for TMDB, Plex, and Drapto mocks to validate metadata formatting and progress flows.

## Migration & Rollout Plan
1. **Foundation**: Establish queue models, config loader, and daemon skeleton; verify `spindled` can start/stop cleanly.
2. **Stage Implementation**: Port identification, ripping, encoding, organizing handlers sequentially, ensuring each passes parity tests.
3. **CLI Parity**: Implement command layer and integrate with daemon RPC; validate output shapes.
4. **Observability**: Wire structured logging, notifications, and metrics.
5. **Cutover**: Provide migration scripts to move existing queue databases (if schema changes). Run side-by-side validation before replacing the Python daemon.

## Open Questions
- None; outstanding decisions (encoder strategy, health checks, configuration defaults) are resolved for the initial Go implementation.
