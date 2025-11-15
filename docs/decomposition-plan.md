# Spindle Content ID & Subtitles Decoupling Plan

This implementation plan charts the steps required to peel the content identification and subtitle generation subsystems out of the main Spindle daemon so they can eventually run as independent workers. Each phase is scoped so the repository stays buildable and the workflow remains usable at all times. Run `./check-ci.sh` (tests + lint) after every substantive change and rebuild/restart the daemon (`go install ./cmd/spindle` + `spindle stop/start`) once new behaviour lands.

## Phase 0 – Baseline & Guardrails

- Capture reference logs for both a known-good TV disc (e.g., South Park Season 5 Disc 1) and a representative movie disc so regressions across media types can be detected quickly.
- Verify caches reside at the expected defaults (`opensubtitles_cache_dir`, `whisperx_cache_dir`).
- Ensure the worktree is clean and CI passes before making structural changes; re-run `./check-ci.sh` at the end of every phase.

## Phase 1 – Define Contracts

- Create `internal/contracts/contentid` and `internal/contracts/subtitles` packages with serializable `Service` interfaces plus `MatchRequest/Response` and `GenerateRequest/Result` structs (only primitive fields plus `ripspec` metadata).
- Update `internal/contentid/matcher.go` and `internal/subtitles/service.go` to satisfy these interfaces without exposing queue/config internals.
- Add unit tests covering contract adapters; run `./check-ci.sh`.

## Phase 2 – Refactor Stage Wiring

- Change `internal/ripping/ripper.go` to accept a `contracts/contentid.Service` in its constructor instead of instantiating `contentid.NewMatcher` internally.
- Update `internal/subtitles/stage.go` so it depends on a `contracts/subtitles.Service` instance supplied by the caller.
- Thread the new dependencies through `workflow.StageSet` wiring (see `cmd/spindle/daemon.go`).
- Validate via integration run (queue item through identify→organize) and `./check-ci.sh`.

## Phase 3 – Modularize Config

- Split `config.Config` into sub-structs (e.g., `WorkflowConfig`, `ContentIDConfig`, `SubtitleConfig`). Provide helper methods so existing code keeps compiling during the transition.
- Update constructors across the repo to accept only the sub-configs they need; maintain backwards-compatible `config.toml` parsing and defaults.
- Rebuild and restart the daemon to ensure runtime config still loads.

## Phase 4 – Extract Shared Libraries

- Move OpenSubtitles utilities (cache/search helpers) into a dependency-light package (e.g., `pkg/opensubs`) so both content-ID and subtitle services can import it without pulling workflow code.
- Do the same for WhisperX command/cuda/transcript-cache helpers (e.g., `pkg/whisperxexec`).
- Add `doc.go` summaries for each new package per AGENTS.md guidelines.

## Phase 5 – Remote-Capable Adapters

- Implement HTTP/gRPC clients in `internal/clients/contentid` and `internal/clients/subtitles` that speak the new contracts.
- Introduce config flags (`contentid_endpoint`, `subtitles_endpoint`) to opt into remote execution; default remains in-process.
- Add proxy stage handlers translating `StageHandler.Execute` calls into remote requests while reusing the same logging and cache paths.
- Extend tests to cover both local and remote adapters.

## Phase 6 – Remote Worker Readiness (Optional)

- Keep Spindle as a single binary by default; ensure all new contracts and adapters can run locally without extra processes.
- Add feature flags or configuration entries that _optionally_ point the content-ID and subtitle services at remote endpoints, but leave them unset so the default build stays monolithic.
- If/when a split is desired, prepare lightweight worker harnesses or documentation describing how to wrap the existing adapters in separate binaries—but defer building/maintaining them until the decision is finalized.

## Phase 7 – Rollout & Cleanup

- Keep remote execution behind configuration flags so the default build/process remains single-binary; document how to enable remote endpoints when/if desired.
- Update `README.md`, `docs/workflow.md`, and `AGENTS.md` with the revised architecture (contracts, config split, cache expectations) while emphasizing that in-process execution is still the default.
- Run end-to-end validation on both the baseline TV disc and movie disc to confirm per-episode `.en.srt` files and correct ordering.
- Remove obsolete glue code once the refactor stabilizes, then run `./check-ci.sh` plus a daemon rebuild/restart as the final acceptance step.
