# System Design: Testing Strategy

Test approach, interface boundaries for mocking, and integration test patterns.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. Principles

- **Table-driven tests** as the default pattern for all logic with multiple cases.
- **No test frameworks** beyond the standard library (`testing`, `testing/fstest`).
  Use `t.Helper()`, `t.Run()` subtests, and `t.Parallel()` where safe.
- **Test what matters**: Business logic, algorithms, and data transformations.
  Skip testing thin wrappers around standard library calls.
- **Golden files** for complex output (SRT generation, robot format parsing,
  notification formatting). Store in `testdata/` directories alongside tests.

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
| `WhisperXRunner` | `transcription` | `Run(ctx, input, opts) (*RawOutput, error)` | `transcription` |
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

---

## 3. Test Categories

### 3.1 Unit Tests

Cover pure logic with no I/O. Run with `go test ./...`.

**High-value targets:**

| Package | What to Test |
|---------|-------------|
| `ripspec` | Envelope parse/encode round-trip, asset tracking, episode key generation |
| `queue` | Stage transitions (valid/invalid), metadata helpers, filename sanitization |
| `textutil` | Tokenization, TF-IDF, cosine similarity, fingerprinting edge cases |
| `contentid` | Hungarian algorithm, anchor selection, block refinement, strategy evaluation |
| `media/audio` | Track selection scoring, channel count parsing, lossless detection |
| `language` | ISO code conversion, normalization, edge cases |
| `subtitle` | SRT parsing, hallucination filtering, SRT alignment, SRT cleaning |
| `encodingstate` | Snapshot marshal/unmarshal, crop parsing, aspect ratio matching |
| `config` | Normalization, validation rules, env var overrides, path expansion |
| `makemkv` | Robot format parsing (TINFO/SINFO/PRGV/MSG lines) |
| `fingerprint` | Hash computation with known inputs |
| `discmonitor` | Label validation, volume ID extraction |
| `notify` | Event formatting, deduplication, suppression rules |
| `opensubtitles` | Candidate ranking, title comparison, edition matching |

### 3.2 Integration Tests (build-tagged)

Tests that require filesystem access, SQLite, or subprocess execution.
Guarded by `//go:build integration`.

| Test Area | Approach |
|-----------|----------|
| SQLite store | In-memory database (`:memory:`), full CRUD cycle |
| Config loading | Temp TOML files, verify parse + normalize + validate |
| Staging cleanup | Temp directories, verify age/orphan logic |
| File operations | Temp files, verify copy + hash verification |
| Rip cache | Temp directories, verify store/restore/prune cycle |

### 3.3 End-to-End Smoke Tests

Not automated. Manual verification by running the daemon with a test disc.
Document expected behavior in `docs/user/workflow.md`.

---

## 4. Test Fixtures

### 4.1 Golden Files

Stored in `testdata/` directories within each package:

```
internal/makemkv/testdata/
  scan_bluray.txt          # MakeMKV robot format output
  scan_dvd.txt
internal/subtitle/testdata/
  hallucination_input.srt  # SRT with known hallucination patterns
  hallucination_expected.srt
  alignment_forced.srt     # Forced subtitle alignment test
  alignment_reference.srt
  alignment_expected.srt
internal/contentid/testdata/
  cost_matrix_3x3.json     # Hungarian algorithm test cases
```

### 4.2 Canned API Responses

JSON files representing external API responses:

```
internal/tmdb/testdata/
  search_movie_response.json
  search_tv_response.json
  season_details_response.json
internal/opensubtitles/testdata/
  search_response.json
  download_response.json
```

---

## 5. Test Helpers

### 5.1 Common Patterns

```go
// testutil package (internal/testutil/) for shared test helpers

func TempDir(t *testing.T) string           // t.TempDir() wrapper
func WriteFile(t *testing.T, path, content) // write + fatal on error
func ReadGolden(t *testing.T, name) string  // read testdata/ file
func AssertEqualSRT(t *testing.T, got, want string)  // SRT comparison ignoring whitespace
```

### 5.2 Store Test Helper

```go
func NewTestStore(t *testing.T) *queue.Store {
    store, err := queue.Open(":memory:")
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { store.Close() })
    return store
}
```

---

## 6. Coverage Goals

No hard coverage targets. Focus testing effort on:

1. **Algorithms**: Content ID matching, audio selection, SRT filtering -- these
   have the highest bug density and are hardest to debug in production.
2. **Data serialization**: RipSpec round-trips, encoding snapshot, metadata JSON.
3. **State machines**: Stage transitions, asset status tracking.
4. **Edge cases in external tool parsing**: MakeMKV robot format variations,
   ffprobe output quirks, WhisperX alignment JSON formats.

Low-value test targets (skip unless bugs emerge):
- HTTP handler routing (thin wrappers around store calls)
- Notification formatting (change frequently, low risk)
- CLI flag parsing (Cobra handles this)
