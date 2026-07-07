# ADR 0002: Task-Graph Scheduler and Task-Centric API

Status: Accepted (implemented 2026-07-03 through 2026-07-05)
Date: 2026-07-07

This ADR distills two completed implementation plans, per the ADR 0001
documentation model. The full phase-by-phase history (measurements,
deviations, validation runs) lives in git history:
`docs/proposals/TASK_SCHEDULER.md` and `docs/proposals/FLYER_REDESIGN.md`.

## Context

A 2026-07-03 audit found throughput was limited by the execution model, not
the tools: whole-disc items advanced through strictly ordered stages, three
capacity-1 semaphores (disc, encoder, WhisperX) were held around entire stage
handlers, stage order did not match data dependencies (subtitle and
commentary work consume RIPPED audio but ran after encoding), and WhisperX
re-transcribed the same audio up to three times per episode. Separately, the
HTTP API flattened everything into one stage string and one progress slot,
forcing flyer to reconstruct the pipeline client-side with ~10 hardcoded
stage maps and heuristics.

## Decision

Schedule (item, stage) tasks against declared resource budgets instead of
advancing items through fixed lanes, and make the tasks table the HTTP API's
display spine.

**Task model.** Tasks are compiled per item at stage granularity from a
static template with explicit dependency edges (`internal/daemonrun`); the
template is a DAG: encoding depends only on identification and streams
ripped assets as they land; the analysis branch (episode ID -> analysis ->
subtitling) reads ripped sources and runs concurrently with encoding; apply
joins both branches and owns every write to encoded files. Task rows are a
projection of `(template, item stage)` — retry and stage moves delete them
and the scheduler recompiles lazily. Tasks are idempotent. Retries are
manual-only. A failed task transitively fails its dependents; sibling
subtrees run to completion.

**Resources.** Claims are declared per stage: `drive`, `gpu`, and `encode`,
capacity 1 each. Episode identification claims the GPU only for TV items.
The scheduler dispatches any task whose dependencies are done and whose
claims fit, waking on task completion.

**Encode workers.** Reel runs in a `spindle encode-worker` subprocess
(same binary re-executed) streaming reporter events as JSON lines, so a cgo
crash fails one job rather than the daemon.

**Concurrency invariants** (any session touching these files must honor):

- Envelope saves are last-writer-wins; overlapping handlers persist envelope
  changes only through merge operations (`stage.Session.MergeSave` and
  helpers) under the per-item lock, never plain Save.
- Only the handler running a task writes that task's progress columns; the
  scheduler writes task state/attempts/timestamps.
- Never parallelize two writers of one file: all in-place MKV rewrites
  (audio refine, disposition, subtitle mux) are serialized inside `apply`.
- Transcription is one batched item-level pass, not per-episode tasks — N
  tasks under a capacity-1 GPU would serialize and re-pay the WhisperX cold
  start per episode. The canonical transcript artifact (staging-scoped,
  recorded in RipSpec) is shared by episode ID, commentary, and subtitles.
- Asset/episode keys are permanent rip-time identifiers (`s01_001`); episode
  matching never renames them. Episode identity lives in envelope episode
  fields and is resolved at organize time.

**API.** Per-task progress lives on task rows (item-level progress columns
and the single-slot arbitration machinery were deleted). `/api/queue` list
responses exclude the raw RipSpec (single-item GET keeps it); curated
projections cover episodes, content ID, and source. `/api/status` exposes
the pipeline template and live scheduler occupancy, which provides the
drive-free signal. Flyer renders all of this data-driven, stays read-only,
and polls; there is exactly one consumer, so the API is not versioned and
breaks forward.

## Rejected and reversed

- **Dynamic planner / DAG library / plugin registry**: the work is fully
  enumerable after identification; plain Go data suffices.
- **Auto-eject after ripping**: operator rejected; the drive-free signal via
  `/api/status` replaced it.
- **Cross-tier encode pairing (1080p+4K), REVERSED 2026-07-07**: the
  2026-07-04 gate measured probe-score identity and +23.9% throughput but
  not peak VRAM of two concurrent CVVDP metric pools; a real 1080p+UHD pair
  exhausted the 16 GiB card and both encodes died. `encode` is capacity 1.
  Do not raise it (or `gpu`) without an A/B gate that includes concurrent
  peak-VRAM measurement at real disc resolutions.
- **Second concurrent WhisperX task**: VRAM would fit, but it only helps
  multi-disc backlogs and deepens encode slowdown; revisit with evidence.
- **SSE/WebSocket push and flyer mutation controls**: one daemon, one TUI,
  2s polls.

## Consequences

- The lane loop, per-stage semaphores, stage-advance model, key-remap
  machinery, and progress arbitration are deleted; net production LOC
  negative across both plans.
- Validated wins (Breaking Bad S1 disc): encoding starts seconds after the
  first title rips; the whole analysis branch completes inside the encode
  window; encode-finish to library is about a minute instead of ~12 minutes
  of serial WhisperX work.
- The queue DB gained the `tasks` table; the DB remains transient with no
  migrations — schema changes mean clear it.
- Old flyer versions do not work against new spindle; that is accepted.
