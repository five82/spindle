# System Design: Testing Strategy

Status: Normative spec.

Test approach, seams, and coverage priorities for the Spindle codebase.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. Principles

- **Table-driven tests** as the default pattern for all logic with multiple cases.
- **No test frameworks** beyond the standard library (`testing`, `testing/fstest`,
  `net/http/httptest`). Use `t.Helper()`, `t.Run()` subtests, and
  `t.Parallel()` where safe.
- **Test what matters**: Algorithms, data transformations, state machines, and
  parsing. Skip testing thin wrappers around standard library calls.
- **Golden files** for complex output (SRT generation, robot format parsing).
  Store in `testdata/` directories alongside tests.

## 2. Test Seams

Production code uses concrete types, not consumer-defined interfaces. The
primary test seams are:

### 2.1 Package-Level Function Variables

External tool calls are wrapped in package-level `var` assigned at init time.
Tests swap these for stubs and restore via `t.Cleanup`:

```go
var inspectMedia = ffprobe.Inspect

func TestFoo(t *testing.T) {
    orig := inspectMedia
    t.Cleanup(func() { inspectMedia = orig })
    inspectMedia = func(ctx context.Context, binary, path string) (*ffprobe.Result, error) {
        return &ffprobe.Result{...}, nil
    }
    // ... test logic ...
}
```

Known seams:

| Variable | Package | Wraps |
|----------|---------|-------|
| `inspectMedia` | `transcription` | `ffprobe.Inspect` |
| `inspectSubtitleMedia` | `subtitle` | `ffprobe.Inspect` |

### 2.2 In-Memory SQLite

Queue store tests open SQLite in `:memory:` mode for fast, isolated state
machine and query tests.

### 2.3 HTTP Test Servers

API client tests (TMDB, OpenSubtitles, LLM, Jellyfin) use `httptest.NewServer`
to provide canned JSON responses matching real API schemas.

### 2.4 Temp Directory Fixtures

Tests that need file artifacts (SRT parsing, rip spec round-trips, content ID
fingerprinting) create test data in `t.TempDir()` directories with known
content.

### 2.5 Functional Tests (Transcription, Formatter)

The embedded WhisperX wrapper script and Stable-TS formatter script are tested
by invoking the script directly with test audio and comparing output against
expectations. These are not mocked; they test the actual Python code path.

## 3. Coverage Priorities

Focus testing effort on high-value areas:

1. **Algorithms**: Content ID matching (`contentid`), audio selection
   (`media/audio`), subtitle filtering/validation (`subtitle`), SRT parsing
   (`srtutil`), and text fingerprinting (`textutil`). These have the highest
   bug density and are hardest to debug in production.

2. **Data serialization**: RipSpec round-trips (`ripspec`), encoding snapshot,
   metadata JSON.

3. **State machines**: Stage transitions, asset status tracking, episode key
   resolution.

4. **External tool output parsing**: MakeMKV robot format (`makemkv`), ffprobe
   JSON (`media/ffprobe`), BDInfo key-value output (`identify`).

5. **Subtitle-specific**: Canonical artifact preservation, display formatting,
   readability validation, hallucination filtering. Prefer golden fixtures for
   real-world bad wrapping and hallucination cases. Test:
   - Canonical transcript artifacts remain unchanged across formatting.
   - Display/subtitle output is derived separately from canonical artifacts.
   - WhisperX wrapper contract: explicit transcription profile, VAD-method
     forwarding, VAD/decode profile arguments, confidence-preserving JSON.
   - Severe validation gating: broken subtitle output fails the episode job and
     does not mux into MKV.
   - Readability repair: fallback cue splitting/wrapping and gap-aware timing
     expansion do not mutate canonical artifacts.
   - Validation thresholds: 20 CPS max reading speed, 5/6s min cue duration,
     7s max cue duration, max 2 lines, 42 chars per line.
   - Aggregate QC metrics: cue counts, max/p95 CPS, high-CPS counts,
     short/long duration counts, overlong/unbalanced line counts.
   - Formatting/QC changes do not perform content rewriting, paraphrasing, SDH
     authoring, or lyric removal.

**Stage handler contract tests** follow a consistent pattern: construct handler
with real or stub dependencies, call `Run(ctx, item)`, assert item state
changes and side effects.

## 4. Cross-Stage Regression

Subtitle formatting and content ID algorithms must not silently regress each
other. Tests that exercise content ID matching should use canonical transcript
artifacts produced by the real transcription path (or golden fixtures of them)
rather than simplified test transcripts.

## 5. Low-Value Test Targets

Skip unless bugs emerge:
- HTTP handler routing (thin wrappers around store calls).
- Notification formatting (changes frequently, low risk).
- CLI flag parsing (Cobra handles this).
