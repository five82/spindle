# Disc Fingerprinting Implementation Plan

## Context

Spindle currently re-discovers a disc every time it is inserted because we only
persist synthetic data like the volume label. That limits our ability to skip
already-processed media, resume interrupted work, or reuse gathered metadata.
Adding a lightweight fingerprint gives us a stable identifier that can thread a
disc through the workflow without introducing a large new subsystem.

## Objectives

- Generate a deterministic fingerprint for each disc using information we can
  access without ripping the entire payload.
- Persist that fingerprint alongside the queue item so we can recognise a disc
  when it returns.
- Use the fingerprint to short-circuit duplicate processing and to seed future
  resume support.

Nice-to-have ideas (full workflow history, smart caching, series correlation)
are explicitly out of scope for the first iteration. We will revisit them once
we have real-world data about how the basic fingerprint behaves.

## Fingerprint Strategy

MakeMKV already derives a per-disc hash that is stable across runs. We will
capture the `disc.fingerprint` value produced by `makemkvcon info` and use it as
our sole identifier. If the scan completes but does not return a fingerprint we
will log a fatal error and bail out, so we notice the problem quickly and can
decide whether a fallback is actually necessary.

1. Capture the `disc.fingerprint` value produced by `makemkvcon info`. It is
   designed for exactly this use-case and avoids reinventing the algorithm.
2. Store the value as a 64-character hex string.

## Data Model Updates

We only need one new column on the existing queue table.

```sql
ALTER TABLE queue_items ADD COLUMN disc_fingerprint TEXT;
CREATE INDEX IF NOT EXISTS idx_queue_disc_fingerprint ON queue_items(disc_fingerprint);
```

The index keeps lookups cheap when a disc is reinserted. No additional tables or
version fields are required for this iteration.

## Workflow Integration

### During Disc Identification

- Extend the MakeMKV scan wrapper to return the MakeMKV fingerprint. If the
  scan finishes without one, emit a fatal log message and abort processing so
  we can investigate the disc or consider adding a fallback later.
- Attach the fingerprint to the queue item as soon as we know it. Persist it
  via `queue.update_item` so the value survives daemon restarts.

### When a Disc Is Detected

- Look up `queue_items` by `disc_fingerprint`.
  - If we find a completed item, emit an info notification and skip the new
    queue entry.
  - If the previous run failed or was mid-process, reuse the row so a future
    resume feature can continue from the last known stage.
  - Otherwise continue with the normal identification flow.

All existing components (disc handler, orchestrator, queue manager) already have
hooks where we can thread this value without introducing a dedicated
fingerprinting service layer.

## Testing Approach

Keep tests focused on the behaviour we care about:

- Unit test the MakeMKV wrapper to ensure we capture the fingerprint and raise
  a fatal error when it is missing.
- Extend the queue tests to confirm that inserting a known fingerprint prevents
  duplicate queue rows.
- Add a small integration test around the disc detection path that mocks
  MakeMKV to supply a fingerprint and verifies we short-circuit the second
  insertion.

We do not need separate performance or memory benchmarks at this stage; the
logic reads a handful of files and writes a single column.

## Documentation Updates

- Mention the new fingerprint behaviour in `AGENTS.md` so future work knows the
  identifier exists.
- Update CLI help text where we surface queue entries to include the optional
  fingerprint for debugging (e.g., `spindle show` could display it when present).

## Deferred Enhancements

Once the minimal fingerprint has shipped and proven useful, we can consider:

- Resume support that rehydrates workflow state based on the fingerprint.
- Caching TMDB identification results keyed by fingerprint.
- Tracking per-disc preferences or multi-disc collections.

Keeping these ideas explicitly staged prevents scope creep while still pointing
at the long-term vision.
