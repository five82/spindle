# Task-Graph Scheduler

Status: Approved plan, implementation not started.
Date: 2026-07-03 (decided with the operator in-session).
Audience: the LLM sessions that will implement this, phase by phase. Read this
file before touching scheduling, stage, or concurrency code. Update the
"Progress" section as phases land. When all phases are complete, distill this
file into an ADR and delete it (see ADR 0001 for the documentation model).

## Why this exists

A 2026-07-03 end-to-end audit found that spindle's throughput is limited by its
execution model, not by its tools:

- The unit of work is the whole disc item. Stages run strictly in order per
  item, and all per-episode work is a serial loop inside one stage invocation.
- Three capacity-1 semaphores (disc, encode, WhisperX) are acquired around
  entire stage handlers, so resources are held while unrelated work runs
  (TMDB calls under the disc semaphore, cache copies with the drive idle).
- Stage order does not match data dependencies. Subtitle transcription,
  commentary classification, and episode matching all consume audio that
  exists in the RIPPED source, yet subtitling and audio analysis run only
  after encoding, and episode identification blocks encoding from starting.
- WhisperX pays a full cold start (uvx + torch import + model load) on every
  invocation, and the same primary audio track is transcribed up to three
  times per episode across episode-ID, commentary similarity, and subtitling.

The fix is to schedule (asset, task) pairs against declared resource budgets
instead of advancing whole items through fixed lanes. Related motivation: the
open cross-title pairing item in reel's `~/projects/reel/docs/PERFORMANCE_TESTING.md`
(run one 1080p and one 4K encode concurrently, projected 15-35% library-level
throughput) explicitly assigns the pairing policy to spindle. That lands here
as Phase 5.

## Decision summary

Build a STATIC per-item task graph with a small greedy scheduler:

- At the end of identification, expand the RipSpec into a fixed list of typed
  tasks with explicit dependency edges and resource claims. The graph is data,
  compiled from a movie or TV template. Log it at compile time.
- The scheduler loop dispatches any task whose dependencies are complete and
  whose resource claims fit the current budget. Tasks run as goroutines,
  except encodes, which become subprocess workers (Phase 5).
- Task state persists in a `tasks` table in the queue DB. Startup recovery is
  "reset running to pending". The queue DB stays transient: no migrations, no
  schema versioning; schema changes mean clear the DB (existing policy).
- The queue UI, HTTP API, and Flyer keep working via a display stage derived
  from task states. The lanes survive as a presentation concept only.

Explicitly rejected (do not build):

- A dynamic planner or agent-style orchestrator that decides at runtime what
  work to spawn. The work is fully enumerable after identification; a dynamic
  planner adds failure states without adding capability.
- Plugin/task registries, generic DAG libraries, or a workflow engine. The
  graph has ~12 task types and two templates. Keep it as plain Go data.
- A permanent dual execution path. The legacy lane path exists only during
  Phase 3 bring-up and MUST be deleted at the end of that phase (AGENTS.md
  complexity budget: production LOC flat or negative; no compatibility
  layers).

## Target architecture

### Task model

Table `tasks` (queue DB): `id, item_id, type, asset_key, state, attempts,
error_message, claims_json, created_at, started_at, finished_at`.

States: `pending`, `running`, `done`, `failed`, `skipped`. Readiness is
derived at query time (all dependency edges `done`), not stored. Edges live
in a `task_deps(task_id, depends_on_id)` table or as a JSON list on the task
row -- implementer's choice, but keep it queryable for logging.

Every task is idempotent: it either detects its output already exists and
returns `done`, or it removes stale partial output and reruns. This replaces
today's per-stage "clean stale outputs" logic.

### Task types and dependency graph (FINAL, Phase 0 2026-07-03)

`[k]` = one task per title/episode asset key. Claims in parentheses.

| Task | Claims | Depends on | Today's code (anchor) |
|------|--------|------------|------------------------|
| `disc_scan` | drive | - | `identify.Identify` drive phase: ProbeDisc, bd_info, `makemkv.Scan` |
| `resolve_metadata` | network | `disc_scan` | `identify` resolve phase: KeyDB, disc ID cache, TMDB search, envelope build |
| `rip_title[k]` | drive | `disc_scan`, `resolve_metadata` | `ripper.ripTitle`; rip-cache hit satisfies ALL rip tasks without the drive claim (`restoreFromRipCache`). The `resolve_metadata` edge is REQUIRED (verified 2026-07-03): TV selection rips only episode-referenced titles from `env.Episodes` (TMDB season data) and movie selection branches on media type; scan-only ripping would hit the unknown-type fallback (rip everything above MinTitleLength), a behavior change. Cost is a couple of HTTP calls; a disc ID cache hit skips TMDB entirely |
(No `eject_disc` task: the operator rejected auto-eject 2026-07-03. Instead,
DEFERRED future work: expose a "drive free" signal via the HTTP API once the
drive claim releases after the last rip, so Flyer can show an indicator that
another disc can be inserted. Not part of any phase yet.)
| `cache_store[k]` | io | `rip_title[k]` | `ripper.cacheFreshRip`, off the critical path; nothing depends on it |
| `transcribe_primary[k]` | gpu | `rip_title[k]` | `transcription.Service`; output is a persisted artifact (see Phase 1). Verified 2026-07-03: episode-ID, subtitle generation, and commentary similarity ALL already use the same model (`Subtitles.WhisperXModel`, default large-v3 -- none of those call sites set `TranscribeRequest.Model`), so one artifact serves all three. Artifact format: keep WhisperX's existing SRT + JSON (word timings) outputs in staging, recorded in RipSpec. PHASE 2 FINDING (2026-07-03): per-episode transcribe tasks would forfeit Phase 1's batching -- N tasks under a capacity-1 GPU budget run serially, each paying the uvx/torch/model cold start batching exists to amortize. Phase 3 must model this as ONE item-level `transcribe_primaries` task depending on all `rip_title[k]` (one batch, all episodes). Transcription is off the encode critical path once analyze/apply splits, so waiting for the last rip costs nothing that matters |
| `fetch_references[k]` | network | `resolve_metadata` | `contentid.fetchReferenceFingerprints` (TV only; keep serial internally for OpenSubtitles rate limits) |
| `episode_match` | network, cpu | all `transcribe_primary`, all `fetch_references` | `contentid` matching + LLM verify (TV only; cross-episode, so it joins all transcripts) |
| `classify_commentary[k]` | gpu, network | `transcribe_primary[k]` | `audioanalysis` phase 1 on RIPPED audio, PER EPISODE (decided 2026-07-03; today it reads encoded output, inspects only `encodedPaths[0]`, and propagates track indices to all episodes -- mislabels discs whose layouts vary). Classification reuses the large-v3 similarity transcript; the separate large-v3-turbo pass and the `Commentary.WhisperXModel` knob get deleted (Phase 1) |
| `encode[k]` | encode, gpu | `rip_title[k]` | `encoder.encodeJob` / reel `EncodeWithReporter` |
| `generate_srt[k]` | gpu, cpu | `transcribe_primary[k]` | `subtitle.GenerateDisplaySubtitle` reading RIPPED audio (today reads encoded) |
| `apply_audio[k]` | cpu | `encode[k]`, `classify_commentary[k]` | `audioanalysis.RefineAudioTargets` + `ApplyCommentaryDisposition` remuxes |
| `mux_subs[k]` | cpu | `apply_audio[k]`, `generate_srt[k]` | `subtitle.MuxSubtitleTrack`; depends on `apply_audio` because both rewrite the same MKV in place -- never parallelize writers of one file |
| `organize[k]` | io, network | `mux_subs[k]` (and `episode_match` for TV naming) | `organizer` per-asset copy |
| `finalize` | network | all `organize` | Jellyfin refresh, staging cleanup, notify |

Movies skip `fetch_references`, `episode_match`. Config can skip
`generate_srt`/`mux_subs` (subtitles disabled) and `classify_commentary`
(commentary disabled) by compiling those tasks as `skipped`.

The reordering wins are visible as absent edges: `encode` does not depend on
`episode_match`; `generate_srt` and `classify_commentary` depend on the rip,
not the encode. Naming needs episode identity only at `organize`.

### Resource model

Budgets are configured capacities; each task type declares claims:

- `drive`: capacity 1. Claimed only while the drive is actually read.
- `encode`: one slot per resolution tier (1080p, 4K), capacity 1 each from
  Phase 5. Until then a single global slot. Tier slots, not utilization
  arithmetic, encode the pairing policy: reel's audit shows 1080p+4K is
  complementary (metric-bound vs encode-leaning) while two same-tier encodes
  contend; "at most one encode per tier" captures that without pretending we
  can sum GPU percentages.
- `gpu_vram_gib`: the hard GPU admission budget, in GiB against the 16 GiB
  card. Claims (2026-07-03): `encode_1080p` = 4 (measured 3.9 per reel),
  `encode_4k` = 6 (measured 5.9), `whisperx` (large-v3, CUDA) = 4 ESTIMATE --
  measure real peak during the Phase 4 coexistence gate and correct this line.
  Budget schedule: Phase 3 sets budget 6 (any single task fits, no pair
  admits -- replicates today); Phase 4 raises to 10 after the coexistence
  gate (whisperx 4 + encode_4k 6); Phase 5 raises to 14 (pair + headroom).
  Utilization is deliberately NOT modeled -- the semaphores' pretense that
  "encoder" and "WhisperX" are separate resources was the old fiction, and
  VRAM + tier slots is the smallest honest replacement.
- `network`, `cpu`, `io`: generous or unbounded initially; they exist so
  claims are declared from day one, not so they constrain anything yet.

### Failure and review semantics (CONFIRMED by operator, 2026-07-03)

- A failed task transitively fails its dependents (`failed` with error
  "dependency failed: <task>"). Sibling subtrees keep running to completion.
- The item reaches a terminal state when no task is pending or running:
  `completed` (all done/skipped), `failed` (any failed). A failed item's
  per-asset outcomes are visible from the tasks table; retry re-pends failed
  tasks only, which the idempotence rule makes safe.
- Retries are MANUAL-ONLY (confirmed 2026-07-03): no automatic retry for any
  task class, including network. Revisit only if real operation shows
  transient-network failures stalling overnight runs, and then bound it to
  network-claim tasks.
- Review remains distinct from failure and becomes per-asset (encode
  validation flags, subtitle quality flags), stored where it is today in
  RipSpec asset status.

### Compatibility surface

- Derive a display stage per item from task states (e.g. "encoding 2/4,
  subtitles 1/4" using the existing "Phase N/M" progress format) so the HTTP
  API, CLI, and Flyer keep a linear-looking view. Check Flyer's actual field
  usage before changing any API field.
- Keep AGENTS.md decision logging: graph compile logs the full task list;
  every dispatch/completion/failure logs `decision_type`/`decision_result`/
  `decision_reason` with `item_id`, `task_type`, `asset_key`.

## Phases

Standing gates for every phase: real discs keep flowing through the stable
path while a phase is in progress; run `./check-ci.sh` before handing back;
each phase's summary reports production LOC delta and what it deleted. Do not
start a phase until the previous one has processed at least a few real discs
cleanly (the project is in its first-real-disc-testing period; correctness
evidence beats speed).

### Phase 0 -- finalize policies (design only, no code) -- DONE 2026-07-03

All four deliverables landed in this file: final task table, confirmed
failure/review/retry semantics, GPU claims and budget schedule, and the
open-question decisions (see "Phase 0 decisions" below). One number remains
provisional: the whisperx VRAM claim (measure at the Phase 4 gate).

### Phase 1 -- task-internal wins (lanes unchanged, all survive the scheduler) -- DONE 2026-07-03, pending real-disc validation

1. WhisperX batch mode: `internal/transcription/whisperx_wrapper.py` accepts
   N audio files per invocation, loading models once. Callers with per-episode
   loops (`contentid.generateEpisodeFingerprints`, subtitle jobs) batch.
2. Transcript artifacts: persist the canonical WhisperX result (SRT + JSON
   word timings) per episode in staging, recorded in RipSpec. Episode-ID
   fingerprinting, commentary similarity, and subtitle generation consume the
   artifact instead of re-transcribing -- all three already use the same
   large-v3 default, verified 2026-07-03. Commentary classification reuses
   the large-v3 candidate transcript from the similarity step: delete the
   separate large-v3-turbo transcription pass and the `Commentary.WhisperXModel`
   config knob (decided 2026-07-03; slight change to the LLM's input text is
   accepted). This is an item-scoped artifact, NOT a revival of the removed
   global persistent cache (commit e1553ec) -- it dies with staging cleanup.
3. (Removed 2026-07-03: auto-eject rejected by operator; a Flyer "drive
   free" indicator is deferred future work, see the task-table note.)
4. Rip-cache transfers become hardlinks (IMPLEMENTED 2026-07-03, deviation
   from the original wording): instead of skipping SemDisc on cache hits --
   unsafe in the lane model because an incomplete TV cache falls through to
   a fresh rip that needs the drive -- both cache directions now hardlink
   with verified-copy fallback (`fileutil.LinkOrCopyFileVerified`). The
   cache-hit SemDisc hold shrinks from a multi-GB copy to ffprobe
   validation, and the fresh-rip cache-store tail is near-instant, so no
   async machinery was needed. INVARIANT: cache entries and staging ripped
   files may share inodes; ripped files must only ever be read, replaced,
   or unlinked -- never modified in place. The true no-drive-claim fix is
   the Phase 3 task graph (rip-cache restore has no drive claim).
5. Overlap `fetchReferenceFingerprints` (network, 3s rate-gated serial loop)
   with the transcription loop inside contentid.

Explicitly deferred: any semaphore-scope restructuring of identify/rip. That
decomposition IS Phase 2/3 work; doing it inside the lane model is throwaway.

### Phase 2 -- task-shaped refactor (zero behavior change) -- DONE 2026-07-03 (replay validated)

Extract each stage's sub-steps into idempotent functions matching the task
table rows: explicit inputs, outputs recorded in RipSpec, no reliance on
being called in stage order. Stage handlers become thin serial loops over
these functions; lanes still schedule everything. Acceptance: a rip-cached
disc replayed end-to-end produces the same library output as before the
refactor (see Validation), and no handler contains multi-step logic that is
not one of the extracted task functions.

### Phase 3 -- scheduler cutover at today's concurrency -- CODE COMPLETE 2026-07-03, real-disc bring-up pending; lane deletion blocks phase completion

1. `tasks` (+ deps) tables; graph compile at end of identification.
2. Scheduler loop replaces the stage poll loop in `internal/workflow`:
   ready set intersect budget, dispatch goroutine, persist transitions.
   Wake on task completion, not only on a 5s timer.
3. Startup/shutdown: reset `running` to `pending` (replaces
   `ResetInProgress` semantics). Disc detection gating (today
   `HasDiscDependentItem`) keys off pending/running drive-claim tasks.
4. Display-stage derivation for API/CLI/Flyer.
5. CRITICAL BRING-UP CONSTRAINT: compile the graph with EXTRA conservative
   edges replicating today's stage order (analysis tasks depend on encode,
   episode_match before encode, etc.) and budgets replicating today's
   exclusivity. The cutover must change the machinery, not the concurrency,
   so any new bug is a scheduler bug, not a race.
6. Keep the lane path behind a temporary config flag during bring-up only.
   Acceptance for this phase INCLUDES deleting the lane path, the flag, the
   old per-stage semaphore code, and `ExecuteWorkflowStage`'s stage-advance
   model. If the flag is still present, the phase is not done.

### Phase 4 -- loosen concurrency one edge at a time

Restructured 2026-07-04 after Phase 3 landed, incorporating what stage-
granularity implementation taught us. Each sub-phase is validated on real
discs before the next. Hazards discovered during Phase 3 design that gate
this phase (do not rediscover these):

- ENVELOPE WRITES ARE LAST-WRITER-WINS. stage.Session loads the RipSpec
  envelope at stage start and every Save writes the WHOLE envelope. Two
  concurrent stages on one item silently lose each other's writes. Any
  same-item overlap needs a coordination decision first (4b).
- EPISODE KEY REMAP RACE. contentid's applyMatches remaps asset episode
  keys (placeholder s01_001 -> s01e03) across the whole envelope once.
  Any stage running concurrently with episode_match that records assets
  under pre-remap keys breaks the key chain. episode_match must stay
  upstream of anything that records per-episode assets until keys are
  remapped at save time. Do NOT reorder episode_match parallel to encode.
- SINGLE-SLOT PROGRESS. progress_stage/percent/message are one set of
  columns; two concurrent stages fight over them and Flyer flickers.
  Overlap needs a progress ownership rule.
- PER-EPISODE TASKS FORFEIT BATCHING (recorded in the task table): model
  transcription as one item-level batched task.
- Cross-item encode+WhisperX GPU sharing has ALWAYS happened (legacy
  semaphores were separate), so 4b's same-item overlap adds no new
  resource regime; 4c is about measuring it properly before raising the
  gpu capacity above 1.

Sub-phases:

4a (infra, ZERO behavior change): DAG-capable task compilation --
   TaskSpec gains explicit dependencies, EnsureTasks compiles arbitrary
   edges, ReadyTasks gates on deps rather than the item in_progress flag,
   and worker tracking moves to per-task granularity (per-item stop
   cancellation and the one-worker-per-item dispatch guard stay until 4b
   deliberately relaxes the guard for parallel branches). Templates stay
   linear, so behavior is identical.

4b decisions (operator-confirmed 2026-07-04): envelope coordination via
   SCOPED SESSION OPS (per-item envelope lock; overlapping branch handlers
   persist only through narrow merge operations; envelope stays the single
   source of truth). ENCODING OWNS item progress during overlap (analysis
   branch is progress-silent; its visibility is logs + task states). NEW
   STAGE NAMES analysis/apply ship now; Flyer compatibility is explicitly
   allowed to break and gets fixed in the post-all-phases Flyer update.
   4a+4b validate together on disc. NOTE: the stage-set change means
   existing queue DBs mid-pipeline recompile incorrectly -- clear queue.db
   when deploying 4b (established policy for schema-semantic changes).

4b (analyze/apply split): the template becomes a DAG per item. After
   episode_match (TV) or ripping (movies): an ANALYSIS branch (batched
   primary transcription from RIPPED audio, commentary candidate
   classification from RIPPED audio, SRT generation from transcripts)
   runs in parallel with the ENCODING branch; an APPLY stage (audio
   refine + commentary disposition remuxes, subtitle mux -- all writers
   of the encoded MKV, serialized by design) follows both, then
   organizing. Requires the coordination decisions (operator sign-off):
   envelope-write strategy for the two branches, progress ownership
   during overlap, and new stage names appearing in the CLI/API/Flyer.
   Commentary track indices measured on the ripped file are valid on the
   encoded file because encoding preserves track count/order; refinement
   only strips tracks in apply, after classification.

4c (GPU coexistence measurement + budget raise): run the reel A/B gate
   (see Validation) against same-item overlap from 4b; record the
   measured WhisperX VRAM peak in the resource model; only then consider
   gpu capacity > 1.

4d (rip-to-encode streaming): per-title rip_title[k]/encode[k] tasks so
   episode 1 encodes while episode 2 rips. Needs the same-item overlap
   machinery from 4b plus per-title task templates; the ripper's
   whole-stage staging wipe must move to a per-item pre-rip task first.

### Phase 5 -- encode subprocess workers and cross-title pairing

1. Move encode execution out of the daemon into a per-encode subprocess (a
   `spindle encode-worker` subcommand or the reel CLI). Motivation: reel is
   linked in-process via cgo today (`internal/encoder`, `reel.New`), so a
   crash kills the daemon, and the vship MITIGATE_MALLOC_ASYNC workaround has
   only been considered cross-process (reel PERFORMANCE_TESTING.md); two
   concurrent encodes in one address space is the untested regime.
2. Progress: the reporter callbacks (`encoder.spindleReporter`) become a
   stdout/socket protocol from the worker; snapshot persistence stays in the
   daemon.
3. Pairing policy: encode claims become per-tier GPU/encode weights sized so
   one 1080p + one 4K fit (VRAM peaks 3.9 + 5.9 GiB on the 16 GiB card, per
   reel's audit) but two 4Ks do not. Coordinate prerequisites with reel's
   open item before enabling.

## Validation

- Replay harness: the rip cache lets the same disc run end-to-end
  deterministically without the drive. Before/after any phase, replay a
  cached movie and a cached TV disc and diff STRUCTURE, not hashes: library
  tree paths, ffprobe stream layouts (codecs, track counts, dispositions,
  languages), subtitle presence, RipSpec asset states, review flags.
  Encode outputs are NOT bit-stable run to run (chunk completion order feeds
  reel's CRF prior -- documented in reel's PERFORMANCE_TESTING.md), so file
  hashes and sizes will legitimately differ; do not gate on them.
- GPU coexistence gate (Phase 4/5): reel's method -- probe-score identity at
  shared (chunk, CRF) points plus probes/chunk, size, jod stats -- comparing
  an encode run solo vs. with concurrent WhisperX/second-encode load. Any
  drift beyond solo run-to-run noise fails the gate.
- `./check-ci.sh` before every handback. Tests may grow freely; production
  LOC should trend flat or negative across the whole plan (the lane
  machinery, per-stage semaphores, and stage-advance plumbing get deleted in
  Phase 3).

## Guardrails for implementing sessions

- Never parallelize two writers of the same file. In-place MKV rewrites
  (audio refine, disposition, subtitle mux) are serialized by dependency
  edges on purpose.
- Never raise `gpu_vram_gib` above 6 (the single-task budget) before the
  Phase 4 coexistence gate has actually been run and its results recorded
  here, including the measured whisperx VRAM peak.
- Do not add task types, generic abstractions, or config knobs beyond this
  document without operator sign-off (AGENTS.md: coordinate major trade-offs;
  do not add config to avoid making a decision).
- Do not keep compatibility layers. Break forward; the queue DB is
  disposable.
- Anchors in this doc are function names, not line numbers; verify with grep
  before relying on them, and fix them here if a refactor renames something.
- Record what each phase actually did (dates, deviations, measurements) in
  the Progress section below. This doc is the cross-session memory; a future
  session will not remember this conversation.

## Phase 0 decisions (operator-confirmed 2026-07-03)

1. Commentary coverage on TV discs: PER-EPISODE classification. Today's
   first-only-and-propagate applies episode 1's commentary track indices to
   every episode (`detectCommentary` on `encodedPaths[0]`, indices fed to
   `RefineAudioTargets` for all paths), which mislabels discs whose track
   layouts or commentary presence vary. Cost accepted: one candidate-track
   transcription + one LLM call per candidate per episode; primary
   transcripts are already shared artifacts.
2. `rip_title` KEEPS the `resolve_metadata` edge. Verified in code: TV
   selection needs `env.Episodes` (TMDB season data), movies need media type;
   scan-only ripping would invoke the unknown-type duration-filter fallback,
   a behavior change with real cost (extra titles ripped). Not revisiting.
3. Retry policy: MANUAL-ONLY, no automatic retries for any task class.
   Retry re-pends only failed tasks (per-subtree resume).
4. `generate_srt` REUSES the `transcribe_primary` artifact -- verified all
   primary-audio consumers already run the same large-v3 model, and the
   WhisperX JSON output carries the word timings SRT generation needs.
   Additionally, commentary classification reuses the similarity step's
   large-v3 candidate transcript; the large-v3-turbo second pass and
   `Commentary.WhisperXModel` are deleted in Phase 1.

Failure semantics confirmed as proposed (siblings continue, transitive
dependent failure, per-asset outcomes and review state).

## Progress

- 2026-07-03: Plan approved and documented. No implementation yet.
- 2026-07-03: Phase 0 complete. Task table finalized, semantics and retry
  policy confirmed, GPU claims set (whisperx VRAM = 4 GiB is an ESTIMATE to
  be measured at the Phase 4 coexistence gate). Next: Phase 1, starting with
  WhisperX batch mode and transcript artifacts.
- 2026-07-03: Phase 1 complete (check-ci.sh green; not yet validated on a
  real disc). Delivered: (1) WhisperX batch mode -- wrapper takes repeated
  --audio/--output-dir/--language triples, models cached per language;
  `transcription.Service.TranscribeBatch` is the implementation and
  `Transcribe` is a batch of one; contentid episode transcription and
  commentary candidate transcription are batched. (2) Transcript artifacts
  -- `ripspec.AssetKindTranscript` (SRT path, audio.json adjacent) in
  `staging/<root>/transcripts/<key>/`, written by contentid (TV) or
  commentary primary transcription (movies), reused by commentary
  similarity and subtitle generation
  (`GenerateDisplaySubtitleRequest.Transcript`); commentary classification
  reuses the candidate similarity transcript; the turbo second pass and
  `Commentary.WhisperXModel` knob are deleted. (3) Rip-cache hardlinks
  (see the Phase 1 item 4 note for the inode invariant). (4) contentid
  overlaps the OpenSubtitles reference fetch with transcription
  (goroutine + buffered channel join). Auto-eject was rejected; a Flyer
  drive-free indicator is deferred. Behavior notes for validation: batch
  transcription failure fails all episodes of the batch (same stage
  outcome as before); a whole-batch candidate-transcription failure
  conservatively marks ALL candidates as commentary (previously per-track);
  per-episode transcription progress messages are now per-batch. Next:
  Phase 2 after real-disc validation of Phase 1.
- 2026-07-03: auditgather + itemaudit skill updated for Phase 1 (transcript
  asset counts in asset_health; skill event list and commentary/subtitle
  guidance refreshed). Flyer audited for Phase 1 compatibility: NO changes
  needed -- its envelope decoding ignores the new transcript asset kind,
  queue stage names are unchanged, progress is rendered as opaque
  message+percent, and the active-episode display falls back to inference
  when spindle reports none (the case during batched transcription). Two
  cosmetic items deferred to the post-all-phases flyer update: the progress
  bar sits near 15% for the whole batched transcription window (can read as
  a hang on long TV discs), and the active-episode marker is inferred
  rather than explicit during that window. Both are spindle reporting
  limits of batching, not flyer defects.
- 2026-07-03: Phase 1 validated on a real disc (Air 2023 Blu-ray, movie,
  item #1): clean audit, correct routing, encoding/subtitle validation
  passed, stereo downmixes excluded rather than kept as commentary.
  Operator approved starting Phase 2.
- 2026-07-03: Phase 2 code complete (check-ci.sh green). Extractions, all
  zero-behavior-change: identify.Identify = scanDisc (task disc_scan, the
  only drive-touching part) + resolveMetadata (task resolve_metadata,
  drive-free); organizer gains placeInLibrary (task organize, dedupes the
  two library branches) and finalize (task finalize: Jellyfin refresh,
  notification, staging cleanup); contentid gains matchEpisodes (task
  episode_match: claim resolution, scope expansion, LLM verify, apply);
  audioanalysis gains applyPostRefinementAudio (disposition half of task
  apply_audio). Audited as already task-shaped, no extraction needed:
  ripper (ripTitle, restoreFromRipCache, cacheFreshRip), encoder
  (encodeJob), subtitle (GenerateDisplaySubtitle, muxSubtitles). Design
  finding recorded in the task table: Phase 3 must model transcription as
  one item-level transcribe_primaries task, not per-episode tasks, to keep
  Phase 1's batching. Remaining acceptance: rip-cache replay of a cached
  disc comparing structural outputs (operator runs this; Air is cached).
- 2026-07-03: Phase 2 replay validated (Air rip-cache replay, item #1:
  clean audit, no WARN/ERROR, validation passed, correct library routing).
  Operator approved starting Phase 3.
- 2026-07-03: Phase 3 code complete (check-ci.sh green incl. race
  detector). What was built: `tasks` table + task layer in internal/queue
  (EnsureTasks/ReadyTasks/StartTask/FinishTask/ResetRunningTasks/
  DeleteTasks); scheduler loop in internal/workflow (dispatches every
  ready task whose claims fit the budget, wakes on task completion with a
  5s timer fallback); stage registration carries resource claims (drive/
  gpu/encode, capacity 1 each -- exact replication of the old semaphore
  exclusivity); item.Stage remains the display/API surface, still written
  by ExecuteWorkflowStage, so CLI/HTTP/Flyer are untouched; startup and
  shutdown reset running tasks alongside in_progress.
  DEVIATION (recorded per the guardrails): tasks are compiled at STAGE
  granularity (7 per item, linear deps), not the fine-grained task-table
  granularity. Rationale: at linear stage granularity task rows are a pure
  projection of (template, item.Stage), so every external position
  mutation (RetryFailed, RetryWithRipSpec, MoveToStage) simply DELETES the
  item's tasks and the scheduler recompiles lazily -- no repair logic, no
  migration, legacy queue DBs just work. Fine-grained rows (rip_title[k],
  encode[k], transcribe_primaries, analyze/apply split) become Phase 4
  template changes on this machinery, made together with the edge
  loosening they exist to serve. Do NOT split tasks without also handling
  per-task resumable state (identification's scan output is in-memory
  today -- splitting disc_scan/resolve_metadata into separate rows
  requires persisting DiscInfo first).
  Also: the scheduler removes two legacy behaviors -- workers no longer
  block invisibly on semaphores (items stay visibly pending until
  resources free), and stage transitions no longer wait for the next 5s
  poll tick (wake-on-completion).
  Bring-up: config [workflow] legacy_lanes = true falls back to the old
  loop (TEMPORARY). Phase completion = real-disc validation (fresh disc +
  rip-cache replay + a retry-after-failure) and then DELETING the lane
  loop, semaphores, and the flag.
- 2026-07-03: bring-up testing (kill during encode -> resume OK) exposed
  two PRE-EXISTING bugs via the stop/retry path, both fixed:
  (1) `queue stop` never cancelled the running stage worker (true in the
  legacy lanes too) -- the zombie encode kept running, and later stomped
  the item's state when it failed. The scheduler now tracks each item's
  worker cancel function, cancels workers of user-stopped items on each
  loop pass (<=5s), and refuses to dispatch an item that still has a live
  worker (StopItems clears in_progress while the worker is exiting).
  (2) StopItems recorded no failed_at_stage, so retry-after-stop restarted
  from identification; the re-run ripping stage then wiped staging
  (including reel's resumable encode state) under the zombie -- observed
  as "encoding failed: no files were encoded" and a from-scratch re-encode
  on the next retry. StopItems now records the stopped stage and retry
  resumes there. The legacy lane path retains bug (1); it is not worth
  fixing in code slated for deletion -- do not user-stop under
  legacy_lanes without restarting the daemon afterward.
- 2026-07-03: Phase 3 validated on real hardware (Air replay, item #1):
  kill-during-encode resumed correctly; stop-during-encode cancelled the
  worker; retry resumed the encode in progress with chunks intact; clean
  final audit (validation 7/7, correct library routing; only historical
  WARN/ERROR from the intentionally cancelled attempt). Operator approved
  Phase 4. Lane path, semaphores, and legacy_lanes flag deleted per
  acceptance.
- 2026-07-04: Phase 4 restructured into sub-phases 4a-4d (see the Phase 4
  section) with the discovered hazards recorded (envelope last-writer-wins,
  episode-key remap race, single-slot progress). Phase 4a implemented
  (check-ci.sh green), zero behavior change: TaskSpec carries explicit
  DependsOn edges (topological order enforced at compile), ReadyTasks
  gates on dependencies and item eligibility only (in_progress is now
  purely a display/detection signal), and worker tracking is per-task
  under each item. Dispatch still allows one live worker per item until
  4b's DAG templates deliberately relax it. DAG compilation and
  parallel-branch readiness are covered by queue tests (diamond template).
- 2026-07-04: 4b infrastructure implemented (check-ci.sh green), still
  zero behavior change with the linear template:
  (1) stage.Session gains merge operations -- MergeSave applies a mutation
  to freshly loaded envelope state under a per-item lock AND to the
  session's own view (dual-apply; adopting fresh state would discard a
  handler's unsaved accumulation); SaveAssetSuccess/SaveAssetFailure are
  merge-based for every caller; MergeAddReviewReason for concurrent-safe
  review flags. RULE: overlap-capable handlers must persist envelope
  changes ONLY through merge ops, never plain Save.
  (2) The executor no longer advances items (WorkflowOptions.NoAdvance):
  the scheduler derives the display stage after the last worker exits --
  earliest not-done task in registration order, or completed -- because
  with DAG templates a completing stage cannot know the next one. The
  NoAdvance success path refreshes the item so racing user stops and
  broken stores surface exactly as before.
  (3) Dispatch only resets progress (StartStage) for the FIRST worker of
  an overlap window, implementing the encoding-owns-progress decision.
  NEXT (4b-ii, the remaining chunk): analysis/apply handler split with new
  stage constants, DAG template in daemonrun, per-episode commentary data
  in ripspec, dispatch guard relaxation for parallel branches, audit skill
  update, then combined 4a+4b disc validation (clear queue.db first).
- 2026-07-04: 4b-ii implemented (check-ci.sh green; operator cleared
  queue.db). The template is now a DAG: episode_identification ->
  (encoding || analysis -> subtitling) -> apply -> organizing.
  Stage changes: audio_analysis renamed to `analysis` (per-episode
  commentary detection from RIPPED sources, progress-silent,
  merge-persisted); `subtitling` is generation-only (ripped/transcript
  inputs, SRTs into staging/subtitles, records merge-persisted,
  progress-silent); new `apply` stage joins both branches and owns every
  encoded-file write (per-episode refine with that episode's commentary
  indices, disposition, duration validation, sidecar placement, muxing,
  subtitled assets). ripspec.AudioAnalysisData gains PerEpisode (aggregate
  lists remain for API/audit displays). The encoder's whole-envelope Save
  is deleted and its review flags are merge-based (it overlaps the
  analysis branch now). Dispatch allows parallel branches per item,
  guarded against STALE workers (live worker whose task row was deleted by
  a retry). Same-item branch overlap is proven by a scheduler test
  (both branch handlers block until both have started). itemaudit skill
  updated for the DAG structure. VALIDATION PENDING: combined 4a+4b real
  disc run -- expect interleaved encode/analysis logs, encoding-owned
  progress, commentary indices remapped in apply, subtitles muxed in
  apply. GPU claims still capacity 1: analysis/subtitling serialize with
  episode-ID cross-item; overlap is only gpu-vs-encode.
- 2026-07-04: 4a+4b VALIDATED on a real run (Air, item #1, fresh queue DB,
  log-verified; zero WARN/ERROR). Encoding and analysis started in the
  same second; the analysis branch (11.5 min commentary detection + 2 s
  subtitle generation via transcript-artifact reuse) completed entirely
  during the encode; the post-encode tail (apply: refine 2 s, mux 1 s;
  organize 10 s) took 13 SECONDS from encode-finish to library, versus
  ~12 minutes of serial WhisperX work before this phase. Stage derivation
  advanced correctly at every transition and the display showed encoding
  during overlap. The organizer's "sidecar subtitle not found" info line
  is pre-existing behavior when subtitles are muxed (sidecar name does
  not match the .subtitled.mkv base), not a 4b regression. Remaining in
  Phase 4: 4c (GPU coexistence measurement + budget raise decision) and
  4d (per-title rip-to-encode streaming).
