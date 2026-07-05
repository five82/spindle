# Flyer Redesign: Task-Centric Observability

Status: Approved direction (operator, 2026-07-05). Implementation not started.
Date: 2026-07-05.
Audience: the LLM sessions that will implement this, phase by phase. Read this
file before touching the HTTP API, item progress columns, or any flyer code.
Update the "Progress" section as phases land. When all phases are complete,
distill this file into an ADR (spindle side) and delete it.

Companion plan: `docs/proposals/TASK_SCHEDULER.md` (Phases 0-5, complete)
introduced the task-graph scheduler this redesign exposes. Its deferred item
"expose a drive-free signal via the HTTP API so Flyer can show an indicator"
lands here.

Repos touched: spindle (this repo, Phase A) and `~/projects/flyer`
(Phases B and C). Reel needs no changes (one optional item, see "Reel").

## Why this exists

A 2026-07-05 audit of flyer and spindle's HTTP API found that flyer is a
client-side reconstruction of spindle's OLD linear pipeline, while spindle now
runs a DAG the API never shows:

- The `tasks` table (per-item task state, attempts, deps, timestamps) is never
  serialized to any endpoint. The scheduler's resource budgets (`drive`, `gpu`,
  `encode_1080p`, `encode_4k`) and live-worker registry are private in-memory
  counters with no accessor (`internal/workflow/workflow.go:62-73`).
- The item's `stage` string plus one single-slot progress object is the entire
  display surface. During rip-and-encode overlap the label is wrong for one of
  the two running branches by construction; `SetProgressSilent`,
  `SetStageLabel`, and `refreshDisplayStage` exist only to arbitrate that
  single slot.
- Flyer compensates with ~10 independent hardcoded stage maps (ordinals,
  icons, 3x duplicated theme colors, sort ranks, name normalization such as
  `identification` -> `identifying`) that assume a strict linear order
  (`internal/ui/detail_progress.go:16`), plus heuristics that guess the active
  episode by matching `Encoding.InputFile` basenames against asset paths
  (`internal/ui/detail_episodes.go:198`) and re-derive episodes from the raw
  rip spec client-side (`internal/spindle/types.go:337`).
- `/api/queue` ships the entire raw rip-spec envelope per item on every
  2-second poll, while the curated projection omits data flyer wants
  (per-episode audio analysis, subtitle QC, content-ID summary, structured
  review reasons, `userStopped`).

## Decision summary (operator-approved 2026-07-05)

One principle: **the tasks table becomes the API's spine.** Spindle stops
flattening the DAG for display; flyer stops modeling the pipeline and renders
what the daemon says.

1. **Per-task progress lives in the `tasks` table.** Item-level progress
   columns are DELETED along with the single-slot arbitration machinery
   (`SetProgressSilent`, `SetStageLabel`, `refreshDisplayStage`). Each running
   handler writes progress to its own task row. This is a net deletion in
   spindle, not just a flyer improvement.
2. **Raw `ripSpec` is dropped from `/api/queue` list responses** (kept on
   `GET /api/queue/{id}` for debugging). The curated episode projection is
   completed so no client ever needs to parse the envelope.
3. Flyer stays **read-only** and keeps **2-second polling**. No SSE.
4. The pipeline template and scheduler resource occupancy are exposed via
   `/api/status`, giving flyer a data-driven stage registry and the
   drive-free indicator.

Explicitly rejected (do not build):

- SSE/WebSocket push. One personal daemon, one TUI, 2s polls; push transport
  adds failure modes without adding capability.
- Flyer mutation controls (retry/stop from the TUI). Flyer is read-only by
  design; the CLI owns mutations.
- Changing task granularity (per-asset tasks). The scheduler's one-task-per-
  stage model stays; per-asset visibility comes from `activeAssetKey` on the
  task row plus the existing per-episode asset projection.
- Compatibility shims. There is exactly one API consumer (flyer) and one
  operator. Break forward: old flyer will not work against new spindle, and
  that is fine. Do not version the API.

## Phase A: spindle — per-task progress and API exposure

### A1. Schema (queue DB is transient: clear it, no migration)

`tasks` table gains:

```sql
progress_percent REAL NOT NULL DEFAULT 0,
progress_message TEXT NOT NULL DEFAULT '',
progress_bytes_copied INTEGER NOT NULL DEFAULT 0,
progress_total_bytes INTEGER NOT NULL DEFAULT 0,
active_asset_key TEXT NOT NULL DEFAULT ''
```

`queue_items` loses: `progress_stage`, `progress_percent`, `progress_message`,
`progress_bytes_copied`, `progress_total_bytes`, `active_episode_key`.

`encoding_details_json` STAYS on `queue_items`: there is exactly one encoding
task per item (it streams all asset keys internally), so the column already
has a single writer and both the CLI and auditgather read it from the item.
Moving it to the task row buys nothing.

The delete-and-recompile invariant (task rows are a projection of
`(template, item.Stage)`; retry/move deletes and recompiles them) gives
progress reset on retry for free — recompiled rows start at zero. Do not add
a separate progress-reset path.

### A2. Write path

- `stage.WorkflowOptions` and `stage.Session` gain the running `*queue.Task`.
  `workflow.runTask` already holds it (`internal/workflow/workflow.go:435`);
  thread it through `executor.go:77` into `NewSession`.
- `Session.Progress(...)` writes the task row (`UPDATE tasks SET
  progress_percent, progress_message, progress_bytes_copied,
  progress_total_bytes, active_asset_key WHERE id = ?`). The
  `WithEncodingDetails` option keeps writing `queue_items.encoding_details_json`
  as today. `SetActiveEpisode` writes `tasks.active_asset_key`.
- DELETE: `Session.progressSilent` + `SetProgressSilent`
  (`internal/stage/session.go:24-29,146-152`, encoder call sites
  `internal/encoder/encoder.go:94,120`), `Store.SetStageLabel`
  (`internal/queue/store.go:474-499`), `Manager.refreshDisplayStage`
  (`internal/workflow/workflow.go:623-665` and its call at `:567`).
  Ripper and encoder now write concurrent progress to different task rows;
  there is nothing to arbitrate.
- Invariant replacing single-slot ownership: **only the handler running a
  task writes that task's progress columns.** The scheduler writes task
  state/attempts/timestamps; handlers write progress. No third writer.
- `item.Stage` remains the scheduler's coarse position (eligibility filters,
  `EnsureTasks` compile position, retry semantics) and still advances via
  `finalizeItem` -> `CompleteStage` when the item goes idle. It is no longer
  refreshed mid-flight for display, and no display code may depend on it.

### A3. API shapes

`ItemResponse` changes (`internal/httpapi/response.go`):

- ADD `tasks []TaskResponse`:

```json
{
  "type": "encoding",
  "state": "running",
  "attempts": 1,
  "error": "",
  "dependsOn": ["identification"],
  "startedAt": "...",
  "finishedAt": "",
  "progress": {"percent": 42.5, "message": "Encoding S01E04", "bytesCopied": 0, "totalBytes": 0},
  "activeAssetKey": "s01_004"
}
```

  `dependsOn` is task TYPES (resolved from dep IDs), not row IDs — flyer
  never sees task IDs. Source: `Store.TasksForItem`.
- ADD `displayTitle` (from `Item.DisplayTitle()`, `internal/queue/item.go:118`
  — flyer currently reconstructs this from metadata JSON).
- ADD `userStopped bool`; REPLACE `reviewReason` (semicolon-joined string)
  with `reviewReasons []string` (from `Item.ReviewReasons()`).
- REMOVE the item-level `progress` object (columns are gone). REMOVE
  `activeEpisodeKey`. Keep `encoding` passthrough.
- REMOVE `ripSpec` from `/api/queue` list responses; keep it on
  `GET /api/queue/{id}` only.
- Complete the episode projection (`buildEpisodes`, response.go:266) so raw
  ripSpec parsing is never needed: add per-episode commentary/excluded-track
  summaries (`AudioAnalysisData.PerEpisode`), subtitle QC
  (`SubtitleGenRecord.ValidationResult/ReviewIssues/SevereIssues`), and a
  `contentId` summary object (`ContentIDSummary`) on the item. Delete dead
  fields: `EpisodeResponse.Progress` (never populated),
  `GeneratedSubtitleDecision` (never set), and the
  `SubtitleSource`/`GeneratedSubtitleSource` duplication (keep one).

`StatusAPIResponse` additions:

```json
"pipeline": [
  {"stage": "encoding", "dependsOn": ["identification"], "claims": ["encode_1080p|encode_4k"]},
  ...
],
"scheduler": {
  "resources": {
    "drive":        {"capacity": 1, "used": 1, "holders": [{"itemId": 3, "task": "ripping"}]},
    "gpu":          {"capacity": 1, "used": 0, "holders": []},
    "encode_1080p": {"capacity": 1, "used": 1, "holders": [{"itemId": 2, "task": "encoding"}]},
    "encode_4k":    {"capacity": 1, "used": 1, "holders": [{"itemId": 5, "task": "encoding"}]}
  }
},
"disc": {"paused": false}
```

- `pipeline` comes from the registered `[]PipelineStage` template
  (`internal/daemonrun/daemonrun.go:206`). For the encoding stage, render the
  claim as the tier alternatives string; flyer treats claims as opaque labels.
- `scheduler` needs a new `Manager.BudgetSnapshot()` accessor. To report
  holders, record claims per (itemID, taskID) at `reserve()` time in the
  existing tracking maps — do not add a new registry, extend `running`'s
  bookkeeping.
- `disc.paused` from `discmonitor.Monitor.IsPaused()` (wire the monitor or a
  narrow func into httpapi's deps). **Drive free** for flyer =
  `resources.drive.used == 0 && !disc.paused`. No new route; it rides the
  existing `/api/status` poll.

### A4. Same-phase consumers (do not leave broken)

- `cmd/spindle/cmd_queue.go`: status/show output switches from item progress
  columns to the task list (compact per-task state + percent; verbose shows
  `activeAssetKey`).
- `internal/auditgather` + `.agents/skills/itemaudit/SKILL.md`: gather task
  rows instead of item progress columns; audit checks reference task states.
- ntfy/notify: unaffected (reads no progress columns).

### A5. Phase A validation gate (operator)

Real-disc run (TV disc preferred, rip-encode overlap window):

- During overlap, `curl /api/queue` shows the ripping task and encoding task
  BOTH running with independent live progress; `/api/status` shows `drive`
  and an encode tier simultaneously held.
- After the last rip, `scheduler.resources.drive.used` drops to 0 while
  encoding continues (the drive-free signal).
- `spindle queue status` CLI output is coherent at every stage; itemaudit
  comes back clean.
- Stop/retry still resets cleanly (recompiled tasks show zero progress).

## Phase B: flyer — core rewrite (types, registry, layout)

- Regenerate `internal/spindle/types.go` against the Phase A shapes. DELETE
  `deriveEpisodesFromRipSpec` and all rip-spec parsing; delete dead decoded
  fields flagged in the audit (`LastItem`, `CorrelationID`, `Fields`, etc. —
  keep only what renders).
- ONE stage registry, fed by `/api/status pipeline` at runtime: display
  order (topological), per-stage icon/color/label. DELETE all of:
  `stageOrdinal`, `pipelineStages`, `normalizeEpisodeStage`,
  `isKnownPipelineStage`, `pipelineStageForStatus`, `statusRank`,
  `processingStatuses`, `stageIcons`, the three per-theme `StatusColors`
  maps, `episodeStageChip`, and the `renderActiveProgress` stage switch.
  Unknown stage names render with a neutral style — flyer must never again
  need a code change when spindle renames a stage. If a name looks wrong,
  fix spindle.
- **Header becomes a resource strip** (replaces stage-count arithmetic):
  `Drive: FREE (insert disc)` / `Drive: #3 ripping` · `GPU: #2 whisperx` ·
  `1080p: #2 s01_004 42%` · `4K: #5 18%`, from `scheduler.resources` joined
  with item tasks. "Processing" counts derive from running tasks, not stage
  names.
- **Queue rows** get a compact task strip (one glyph per task in pipeline
  order, colored by task state) instead of the single stage icon. Sort by
  (needs-review, has-running-task, id).
- **Detail pane "Pipeline" section becomes a task board**: one row per task
  from `tasks[]` — state glyph, type, progress bar + message for running
  tasks, attempts/error for failed, duration for done.

Validation gate: run against a live daemon on a real disc; during overlap the
header shows drive + encode tier held and the task board shows two running
rows with independent progress.

## Phase C: flyer — right details at the right time

- Detail sections key off **running tasks**, not item stage. Each running
  task contributes its own section; two sections render during overlap
  because two things are happening: ripping -> title/bytes progress;
  encoding -> the `encoding` snapshot (frames/fps/ETA/size/validation);
  episode_identification / analysis / subtitling -> their task progress
  message plus relevant projection data (match confidence, per-episode
  commentary, subtitle QC); apply -> per-episode refinement; completed items
  -> results/validation summary. The active episode comes from
  `task.activeAssetKey` — DELETE `describeActiveEpisode` and all path-match
  guessing.
- **TV items get an episode-by-task grid** (rows = episodes, columns = rip /
  encode / subtitle / final, from the episode projection's per-asset
  status), replacing `countEpisodesForPipelineStage` and its compensating
  logic.
- Problems view: lead with the failed TASK (type, attempts, error) and
  structured `reviewReasons` list.
- Colorize log lines from structured `LogEvent` fields directly; delete the
  format-then-regex-reparse round trip in `logs.go`.

Validation gate: full TV disc end to end; operator review of every detail
context (pending, active w/ overlap, review, failed, completed).

## Reel

No required changes. OPTIONAL (defer until an operator ask): a reporter event
for target-quality search telemetry ("probing CRF 32, chunk 3/12") so the
pre-encode analysis window shows more than a substage string. Would flow
through the existing Phase 5 wire as a new event and land in
`encodingstate.Snapshot.Substage` detail.

- 2026-07-05: PHASE B IMPLEMENTED (flyer commit 629a1ff, pushed).
  types.go regenerated against the Phase A shapes (rip-spec
  derivation shim and dead fields deleted); ONE stage catalog
  (internal/ui/stages.go) + task board (internal/ui/taskboard.go) replace
  all ten hardcoded stage maps, the pipeline checklist, the separate
  activity bar, and the active-episode path-matching heuristics; header
  gained the resource strip with the drive-free indicator; queue rows show
  per-task glyph strips; per-task durations/ETAs come from server-side
  task timestamps (client-side stageFirstSeen tracking deleted).
  Spindle addition: ItemResponse.source (primary title name/duration) so
  movie detail no longer needs the raw envelope. Flyer check-ci.sh fully
  green. LOC: ~900 added / 1826 deleted (net about -900 with tests).
  Verified against the live daemon with the completed validation item:
  header strip (Drive: FREE), task strip, task board with real durations,
  episodes summary, problems view all render. LIVE-OVERLAP RENDERING
  UNVERIFIED until the next disc runs. Known Phase C work: "Focus: No
  active episode" noise on terminal items; detail sections still keyed to
  one context rather than per running task; episode-by-task grid.

- 2026-07-05: PHASE C IMPLEMENTED (flyer commit edceeee, pushed). Detail sections now key off RUNNING tasks: each
  running task contributes its own section (encoding -> specs/config/
  size/crop; episode_identification -> content-ID match stats; analysis ->
  audio/commentary; subtitling -> generated-so-far; organizing -> file
  states; any task -> its active episode + track). Overlap windows render
  multiple sections at once. Focus renders nothing when nothing is active
  and is dropped from completed items (kills the "No active episode"
  noise). Episode rows gained the per-asset grid (R/E/S/F cells:
  done/active/failed/pending from paths + running tasks' asset keys; pure
  derivation function, unit tested); the redundant file-states text line
  under each row was removed. Problems view leads with the failed task
  (label, attempts, error; failedAtStage fallback). Log lines are styled
  directly from structured LogEvent fields -- the format-then-regex-reparse
  round trip and its five regexes are deleted (problems' warn/error tail
  migrated to structured events too). Flyer check-ci.sh fully green;
  verified against the live daemon on the completed item (task board,
  episode grid, problems, no Focus noise). Live-overlap rendering of the
  per-task sections still validates with the next disc. Phase C diff:
  +282/-149 production, +75 tests.

## Hazards

- **Task rows are deleted and recompiled** on retry/move. `/api/queue` may
  observe an item with zero task rows for an instant; flyer must render that
  as "recompiling" (neutral), never crash or infer completion. Build each
  item's response from one `TasksForItem` read; tolerate the transient at 2s
  poll granularity.
- **Completed/failed items keep their final task rows** (only Clear/Remove
  delete them), so completed detail can still show durations — but do not
  make any view REQUIRE tasks for terminal items.
- ~~**`Stats()` stage counts get coarser** mid-flight~~ RESOLVED 2026-07-05
  during Phase A validation (operator caught `spindle status` reporting
  `ripping 1` mid-encode): `Store.Stats()` now counts each item under its
  earliest RUNNING task's type (terminal stage for failed/completed, item
  stage for idle items), so `/api/status` queueStats is task-aware for every
  consumer. Flyer's Phase B header may still prefer per-task counts for the
  resource strip, but the stage counts it gets are no longer misleading.
- The Phase 4/5 hazards in TASK_SCHEDULER.md still apply to any session
  touching these files: envelope saves are last-writer-wins (use
  MergeSave/merge helpers), and single-writer-per-task-row is the new
  progress invariant.
- LOC expectations (AGENTS.md complexity budget): spindle Phase A roughly
  flat (schema/API additions offset by deleting the arbitration machinery
  and dead projection fields); flyer Phases B-C net negative (the ten stage
  maps, derivation shim, and heuristics come out).

## Progress

- 2026-07-05: Plan approved by operator (per-task progress in tasks table;
  drop raw ripSpec from list endpoint; polling kept; flyer read-only).
- 2026-07-05: Phase A core implemented. Deviations/decisions made during
  implementation (all consistent with the plan's intent):
  - `Store.StartStage(item)` dropped its stage parameter -- it only marks
    in_progress now.
  - `Store.UpdateProgress` and `Store.SetStageLabel` deleted; new
    `Store.UpdateTaskProgress(task)`. `CompleteStage` no longer writes
    progress columns.
  - `stage.NewSession` takes the running `*queue.Task` (4th param); nil
    means a detached in-memory task (OneShot CLI cache path: progress is
    not persisted there, by design -- no scheduler task exists).
  - Handlers that echoed `item.ProgressPercent/Message` back into
    `Progress()` now echo `sess.Task.Progress*`.
  - `HasDiscDependentItem` (disc-detection gate) moved to the tasks table:
    a RUNNING identification/ripping task means the drive is busy. This
    replaces the correctness role `refreshDisplayStage` played -- the gate
    now releases the moment the last rip task finishes, regardless of the
    item's lagging stage label. Note: the OneShot CLI cache rip holds no
    task row and therefore does not hold this gate (it never coexists with
    a running daemon on one drive).
  - `Task.StartedAt/FinishedAt` are now scanned (strings, like Item
    timestamps) and exposed on TaskResponse.
  - Dead API surface deleted while in there: `WorkflowStatus.LastItem`
    (StatusTracker simplified to lastError+deps),
    `EpisodeResponse.Progress`, `GeneratedSubtitleSource/Language/
    Decision` (SubtitleSource/Language remain).
  - Episode `Active` flags derive from running tasks' ActiveAssetKey.
  - Scheduler exposure: `Manager.SchedulerSnapshot()` (holders recorded at
    reserve time under budgetMu) and `Manager.PipelineInfo()` (resolves the
    linear-default deps); httpapi.New gained pipeline + scheduler params.
  - Episode projection completed: per-episode `commentaryTracks`/
    `excludedTracks` counts, `subtitleValidation`/`subtitleReviewIssues`/
    `subtitleSevereIssues`, and item-level `contentId` summary.
  - Consumers updated: auditgather ItemSummary carries per-task summaries
    and `ReviewReasons []string`; itemaudit SKILL.md describes the task
    model; CLI `queue list -v`/`queue show` render per-task progress lines.
- 2026-07-05: PHASE A IMPLEMENTED. Full check-ci.sh green (tests, -race,
  CGO build, lint, govulncheck).
- 2026-07-05: PHASE A VALIDATED on a real disc (Breaking Bad S1 D1, fresh
  queue DB, 126 min end to end):
  1. Overlap: /api/queue showed ripping (2.6%, s01_001) and encoding
     (stream-waiting) as independent running task rows 51s into the rip;
     later encoding 19.3% alongside episode_identification 15% (batched).
  2. Drive-free signal: after the last rip, scheduler.resources.drive went
     used=0/holders=[] with disc.paused=false while encoding continued --
     the deferred TASK_SCHEDULER.md drive-free item works end to end.
  3. CLI coherent (per-task Progress lines with asset key). Operator
     caught `spindle status` counting the item under the lagging stage
     label; fixed same-day by making Store.Stats() task-aware (see the
     resolved hazard below). Cosmetic nit deferred to Phase B/C era: the
     encoding task shows an empty progress message while stream-waiting
     ("Waiting for ripped assets" would read better).
  4. itemaudit clean: 3/3 assets healthy at every stage (incl. transcripts,
     subs muxed), identical episode profiles, validation 7/7 passed,
     77.7% size reduction, zero ERROR log lines. Only findings: the KNOWN
     pre-existing pilot-selection issue (tv_title_selection_ambiguous;
     operator handles separately) -- correctly surfaced through the new
     reviewReasons array.
  5. Stop/retry progress reset: covered by tests; not exercised on disc.
  Also closed: first daemon-driven encode-worker run on a real disc
  (3 subprocess encodes) -- half of TASK_SCHEDULER.md Phase 5's pending
  production validation. Remaining from that plan: observe a UHD+BD
  cross-tier paired encode.
