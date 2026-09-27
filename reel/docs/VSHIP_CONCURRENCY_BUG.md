# Upstream finding: VSHIP CVVDP scores corrupt under concurrent handlers (async allocator race)

**Status:** documented here for the record; **not yet filed upstream.** Reel ships a working
local mitigation (see "Workaround"). This doc is written so it can be turned into a Vship issue
later with minimal edits.

**Upstream project:** Vship — https://codeberg.org/Line-fr/Vship
**Observed on:** Vship 5.1.0 (HEAD `b8e6a4e`, 2026-06-16), CUDA backend, NVIDIA RTX 5060 Ti,
CUDA 13.3, Linux. The HIP/AMD backend uses the same async-allocator path and is very likely
affected too (see below).

## Summary

Running **more than one CVVDP handler concurrently** (one per thread, the pattern Vship's own
API docs recommend) produces **silently corrupted scores** on roughly half of runs. The fault
is the default `cudaMallocAsync`/`hipMallocAsync` allocator: each handler allocates its
per-frame device buffers from a **process-global, stream-ordered memory pool** shared by all
handlers' streams. Concurrent allocate/free across handlers races that shared pool and corrupts
results. Compiling Vship with `MITIGATE_MALLOC_ASYNC` (which swaps the async allocator for
synchronous `cudaMalloc`/`cudaFree`) eliminates it completely.

## Symptom

- With N>1 CVVDP handlers each driven by its own thread, CVVDP scores become wrong on ~50% of
  runs — nondeterministic run-to-run, with a characteristic false **low** ("sub-floor") score on
  affected frames/windows.
- One handler used by one thread is always correct and deterministic.
- It is **not** a host-side framing/decoding bug: the same decoded frames scored by one handler
  give stable, correct results.

## Affected use is the *recommended* use

Vship's C API explicitly intends concurrent handlers:

- `src/VshipAPI.h:119` — "for maximum throughput, it is recommend to use 3 SSIMU2Handler with
  each a thread to use in parallel"
- `src/VshipAPI.h:116`, `:141` — per-handler error accessors exist "for multithreaded scenarios
  (ie one thread per handler)"
- `FFVship` itself spawns N gpu-worker threads, each owning its own handler.

So this is a correctness gap in the supported concurrent-handlers path, not a misuse.

## Root cause

Each `cvvdp::CVVDPComputingImplementation` owns isolated per-handler resources (its own
`stream1`/`stream2`, events, and temporal accumulators), which is correct. But its per-frame
device memory is allocated with the **stream-ordered async allocator**:

- `src/HIP/cvvdp/main.hpp:39,45` and `:268,273` — `hipMallocAsync(&mem_d…, stream1/stream2)`
- `src/HIP/cvvdp/main.hpp:121,145` and `:328,329` — matching `hipFreeAsync(…, stream)`

`hipMallocAsync`/`hipFreeAsync` resolve to `cudaMallocAsync`/`cudaFreeAsync`
(`src/HIP/util/preprocessor.hpp:74-75`). Those draw from a **device-global default memory pool**
shared across every stream and therefore every handler in the process. Two handlers on two
threads allocating/freeing per-frame buffers concurrently race that shared pool; the symptom is
consistent with the pool handing overlapping/recycled device memory to coexisting handlers, so
one handler's buffer is clobbered by another's.

Two details from the diagnosis pin it to the allocator rather than kernel concurrency:

1. **Serializing the GPU *compute* across handlers does not fix it** — only collapsing to one
   handler, or removing the async pool, does. So the corruption rides on the alloc/free path and
   the shared pool, not on concurrent kernel execution.
2. **Optimization level is irrelevant** — the corruption is present in an optimized `-O3` build,
   not just an unoptimized one; `-O3` only changes the failure *rate*.

Shared process-global state that is consistent with the race living at the allocator/pool level
(not in the per-handler objects): `global_gpu_id` (`src/VshipLib.cpp:41`), the global
`HandlerManagerCVVDP` resource manager whose mutex guards only its free-list
(`src/VshipLib.cpp:52`), and the single device default memory pool.

## Evidence / reproduction

Controlled A/B of libvship builds that differ only in the allocator, scoring the same near-floor
4K clip with 1 handler (truth) vs N distinct concurrent handlers:

| libvship build | allocator | concurrent result (4 handlers, near-floor 4K) |
|---|---|---|
| default | `cudaMallocAsync` | **cascades** — up to 56 of 110 chunks corrupted, run-to-run delta up to 2.0 JOD, ~50% of runs |
| `MITIGATE_MALLOC_ASYNC` | sync `cudaMalloc` | **8/8 reps byte-identical to single-handler truth**, 0 corrupted |

Reproduced at both 4 handlers (4K) and 8 handlers (1080p, milder but present). A standalone
reproducer lives in the reel repo: `scripts/handlertest` (scores a kept workdir's chunks with one
handler serially vs N distinct handlers concurrently and reports any divergence). The distilled
Reel decision is in `docs/PERFORMANCE_TESTING.md` under "Concurrent VSHIP handlers"; the
restoring implementation is commit `ec7faf7`.

## Workaround (what reel does today)

Build libvship with `MITIGATE_MALLOC_ASYNC` — e.g. `make build BACKEND=Cuda
MITIGATE_MALLOC_ASYNC=on`. This replaces `cudaMallocAsync`/`cudaFreeAsync` with synchronous
`cudaMalloc`/`cudaFree` (`src/HIP/util/preprocessor.hpp:69-76`), removing the shared async pool.
Reel relies on this and documents it as a hard build requirement. Measured cost in reel's
workload: none — the synchronous-allocator build was if anything marginally faster, because
per-frame allocation was never the bottleneck.

## Suggested upstream fix

The `MITIGATE_MALLOC_ASYNC` flag already exists as a global, all-or-nothing mitigation. A cleaner,
default-safe fix would give each handler an **isolated allocator** so concurrent handlers never
share a pool, while keeping async-allocator performance:

- Create a **per-handler `cudaMemPool_t`** (one memory pool per `CVVDPComputingImplementation`)
  and allocate via `cudaMallocFromPoolAsync` on the handler's own pool, or
- otherwise ensure the per-frame device allocations cannot be shared/recycled across handlers
  (e.g. pre-allocate per-handler scratch once at handler init and reuse it, avoiding per-frame
  async alloc/free entirely).

Either removes the cross-handler coupling that `MITIGATE_MALLOC_ASYNC` currently fixes by
disabling async allocation globally. The HIP/AMD path (`preprocessor.hpp:87-91`, whose mitigation
comment is "avoid amd driver issues") should get the equivalent treatment.

## Decision

Documented and mitigated; **not filing upstream for now.** If revisited, this doc is the draft
issue. Reel's correctness depends on the `MITIGATE_MALLOC_ASYNC` build until/unless upstream
isolates per-handler allocation.
