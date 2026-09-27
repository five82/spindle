# Reel performance suite

This directory contains the reproducible performance harness and clip manifest.
For prior results and reasons not to repeat an experiment, read
`docs/PERFORMANCE_TESTING.md` first.

The repository stores recipes and tools. Clip bytes and run outputs live under
`$REEL_TESTING_DIR` (default `~/testing`) so personal media, workdirs, and large
traces are not committed.

## Files

| File | Role |
|------|------|
| `clips.tsv` | Contiguous-cut recipes: abbreviation, length, resolution, dynamic range, timecodes, output name, and source. |
| `run-suite.sh` | Runs Reel sequentially and captures build/environment metadata, wall, size, GPU/VRAM, host telemetry, `perf.json`, and target-quality logs. |
| `analyze.py` | Summarizes phase timing, worker history, probe counts, stop reasons, quality, and encode-versus-metric work into `summary.json`. |
| `compare-runs.py` | Compares wall, size, probes, and quality between two run directories. |

Related tools:

- `scripts/compare-tq.py`: per-chunk target-quality comparison.
- `scripts/fullvalidate`: whole-chunk CVVDP validation of a kept workdir.
- `scripts/chunkbench`: shot-detection and chunk-planning benchmark.
- `scripts/handlertest`: concurrent VSHIP-handler correctness check.

## Usage

```bash
# Build the exact binary that will be measured.
go build -trimpath -o reel ./cmd/reel

# Historical default matrix shape.
scripts/perf/run-suite.sh --label baseline

# Broad target-quality behavior coverage.
scripts/perf/run-suite.sh --matrix coverage --label tq-coverage

# 4K encoder and memory-bandwidth work.
scripts/perf/run-suite.sh --matrix encoder --label encoder-ab

# Longer clips for startup/serial phases and steady-state SDR SSIMU2.
scripts/perf/run-suite.sh --matrix long --label long-baseline

# Fixed CRF or a custom Reel option.
scripts/perf/run-suite.sh --mode crf --crf 30 --label crf30 sully-5m kbv1-5m
scripts/perf/run-suite.sh --label mw4 sully-5m -- --metric-workers 4

# Summarize and compare.
scripts/perf/analyze.py "$REEL_TESTING_DIR/perf-runs/<timestamp>-baseline"
scripts/perf/compare-runs.py <run_A> <run_B>
```

By default the suite deletes bulky `.reel-*` workdirs after harvesting the
small artifacts. Pass `--keep-workdirs` when the experiment needs independent
validation or probe-IVF inspection.

## Matrices

| Matrix | Clips | Use |
|--------|-------|-----|
| `default` | `air-5m im-5m bts-5m sully-5m kbv1-5m sullyhv-15m` | Historical A/B shape. Preserve it for continuity, but do not assume an old run is a valid baseline for current code. |
| `coverage` | `air-5m bts-5m im-5m soms-5m io-5m sully-5m kbv1-5m ko-5m sullyhv-15m` | Broad TQ coverage across clean/grainy SDR, clean/grainy 4K, CG, and hard content. |
| `encoder` | `sully-5m kbv1-5m ko-5m sullyhv-15m` | Encoder-side changes where 4K encode and memory-bandwidth behavior matter. |
| `long` | `air-20m bts-20m sully-20m ko-20m` | Serial/startup phases and SDR steady state that five-minute warmup-dominated clips cannot show. |

Corpus roles:

- `air`: clean/light 1080p SDR.
- `im`: moderate 1080p SDR.
- `bts`: grainy, low-CRF 1080p SDR.
- `soms`: light-grain 1080p SDR.
- `io`: clean CG 4K HDR.
- `sully`: normal clean 4K HDR.
- `kbv1`: grain-heavy 4K HDR.
- `ko`: grain-emulation 4K encode/memory-bandwidth stress.
- `sullyhv`: derived hard-content stress asset, not one contiguous manifest cut.

## A/B validity

- Run variants sequentially on the single GPU. Concurrent workloads make wall,
  utilization, thermals, and VRAM incomparable even when scoring remains
  correct.
- Use a separate fresh run directory for every variant. The harness clears each
  clip's workdir because Reel resume can otherwise reuse chunks from a previous
  configuration.
- `run-meta.json` records the binary hash, git state, hardware, driver, and
  linked-library paths. It cannot detect whether libvship was built with
  `MITIGATE_MALLOC_ASYNC`; verify that separately with `scripts/handlertest`
  after libvship/GPU/driver changes.
- Target-quality output SHA-256 is not expected to match across runs. Completion
  order changes prior availability and can legitimately alter probe paths. Gate
  score-path correctness on shared `(chunk, CRF)` probe scores, then compare
  probes/chunk, stop reasons, size, and delivered quality.
- For changes that can affect quality, keep the workdir and run
  `scripts/fullvalidate`. Do not modify or replace the source between encode and
  validation.
- Recorded scores are exact whole-chunk scores in the metric used for that
  probe, and the chosen IVF is reused as the final chunk. On automatic SDR
  <=1080p runs, warmup chunks are CVVDP and later chunks are SSIMULACRA2;
  `fullvalidate` supplies a separate all-CVVDP policy check rather than an
  identity check against the SSIMULACRA2 values.
- Worker history in artifacts before 2026-07-01 was completion-sampled and can
  under-report active/in-flight peaks. Current artifacts use timer sampling.

## Artifact layout

Each run directory contains:

- `run-meta.json`: matrix, clip tokens, exact binary/source state, and machine.
- `results.tsv`: return code, wall, output size/hash, and GPU summary per clip.
- `summary.json`: analyzer output after `analyze.py` is run.
- `<clip>/<clip>.log`, `.gpu`, `.host`: Reel output and sampled telemetry.
- `<clip>/perf.json`: harvested pipeline phases and worker history.
- `<clip>/target-quality.json`: harvested aggregate search decisions.
- `<clip>/.reel-*/tq/*.json`: per-chunk search decisions when
  `--keep-workdirs` is used.

Keep only distilled decisions in `docs/PERFORMANCE_TESTING.md`; leave large raw
artifacts here under `$REEL_TESTING_DIR`.
