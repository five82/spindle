# System Design: Testing Strategy

Test approach and interface boundaries for test doubles.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. Principles

- **Table-driven tests** as the default pattern for all logic with multiple cases.
- **No test frameworks** beyond the standard library (`testing`, `testing/fstest`).
  Use `t.Helper()`, `t.Run()` subtests, and `t.Parallel()` where safe.
- **Test what matters**: Business logic, algorithms, and data transformations.
  Skip testing thin wrappers around standard library calls.
- **Golden files** for complex output (SRT generation, robot format parsing).
  Store in `testdata/` directories alongside tests.

---

## 2. Interface Boundaries for Test Doubles

External tools and services are the primary testing boundary. Each external
dependency gets a consumer-defined interface at the call site, allowing test
doubles without modifying production code.

### 2.1 External Tool Interfaces

These wrap CLI tools that stage handlers invoke:

| Interface | Package | Methods | Used By |
|-----------|---------|---------|---------|
| `MakeMKVRunner` | `makemkv` | `Scan(ctx, device) (*ScanResult, error)`, `Rip(ctx, device, titleID, outDir) error` | `identify`, `ripper` |
| `FFprobeRunner` | `media/ffprobe` | `Inspect(ctx, path) (*Result, error)` | Multiple stages |
| `TranscriptionRunner` | `transcription` | `Run(ctx, input, opts) (*RawOutput, error)` | `transcription` |
| `MkvmergeRunner` | `subtitle` | `Mux(ctx, input, subs, output) error` | `subtitle` |

In production, these are concrete structs that shell out via `exec.CommandContext`.
In tests, they are replaced with stubs that return canned responses.

### 2.2 Service Client Interfaces

REST API clients defined at the consumer site:

| Interface | Defined In | Methods | Wraps |
|-----------|-----------|---------|-------|
| `TMDBSearcher` | `identify` | `SearchMovie(...)`, `SearchTV(...)`, `GetSeasonDetails(...)` | `tmdb.Client` |
| `SubtitleFetcher` | `contentid`, `subtitle` | `Search(...)`, `Download(...)` | `opensubtitles.Client` |
| `LLMClassifier` | `identify`, `audioanalysis`, `contentid` | `Classify(ctx, prompt) (*Response, error)` | `llm.Client` |
| `LibraryRefresher` | `organizer` | `Refresh(ctx) error` | `jellyfin.Client` |
| `Notifier` | `workflow` | `Notify(ctx, event) error` | `notify.Service` |

### 2.3 Infrastructure Interfaces

| Interface | Defined In | Methods | Wraps |
|-----------|-----------|---------|-------|
| `ItemStore` | `stage` (or consumer) | `Update(item)`, `UpdateProgress(item)`, `GetByID(id)` | `queue.Store` |
| `TranscriptionService` | `contentid`, `subtitle` | `Transcribe(ctx, req) (*Result, error)` | `transcription.Service` |

### 2.4 Test Double Summary

| Interface | Double Type | Key Behavior |
|-----------|-------------|--------------|
| `queue.Store` | In-memory fake | SQLite in `:memory:` mode |
| `transcription.Service` | Stub | Return canned canonical transcription artifacts (SRT + JSON) |
| `makemkv.Runner` | Stub | Return canned robot output |
| `tmdb.Client` | Stub | Return canned search/detail JSON |
| `opensubtitles.Client` | Stub | Return canned subtitle content |
| `llm.Client` | Stub | Return canned classification |
| `jellyfin.Client` | Stub | No-op or record calls |
| `notify.Client` | Stub | Record sent notifications |
| `ffprobe.Runner` | Stub | Return canned probe output |
| `drapto.Client` | Stub | Return canned encode result |

Stage handler tests follow a consistent pattern: construct handler with stub
dependencies, call `Run(ctx, item)`, assert item state changes and side effects.

---

## 3. Coverage Goals

No hard coverage targets. Focus testing effort on:

1. **Algorithms**: Content ID matching, audio selection, subtitle filtering/formatting, and SRT validation — these
   have the highest bug density and are hardest to debug in production.
2. **Data serialization**: RipSpec round-trips, encoding snapshot, metadata JSON.
3. **State machines**: Stage transitions, asset status tracking.
4. **Edge cases in external tool parsing**: MakeMKV robot format variations,
   ffprobe output quirks, transcription SRT/JSON output formats, subtitle formatter invocation.

**Subtitle-specific expectations:**
- Test that canonical transcript artifacts remain unchanged when subtitle formatting runs.
- Test that display subtitle output is derived separately from canonical cached transcript artifacts.
- Add cross-stage regression coverage so subtitle formatting changes cannot silently change episode-identification inputs.
- Prefer golden fixtures for real-world bad wrapping / hallucination cases.

Low-value test targets (skip unless bugs emerge):
- HTTP handler routing (thin wrappers around store calls)
- Notification formatting (change frequently, low risk)
- CLI flag parsing (Cobra handles this)
