# Usage Guide

Run `reel encode --help` for the authoritative flag list. The sections below provide practical context.

## CLI Basics

```bash
# Basic encode
reel encode -i input.mkv -o output/

# Batch encode an entire directory
reel encode -i /videos/ -o /encoded/

# Default target-quality encode (source-selected metric via VSHIP/CUDA)
reel encode -i input.mkv -o output/

# Fixed CRF mode
reel encode -i input.mkv -o output/ --quality-mode crf --crf 24.25 --preset 6

# Resolution-specific fixed CRF (SD, HD, UHD)
reel encode -i input.mkv -o output/ --crf 24,26.25,26.5

# Verbose output
reel encode -v -i input.mkv -o output/
```

## Frequently Used Options

**Required**
- `-i, --input <PATH>`: Input file or directory containing video files
- `-o, --output <DIR>`: Output directory (or filename when single file)

**Quality Settings**
- `--quality-mode target|crf`: target-quality mode is the default in normal builds; `crf` is the default in `no_vship` builds and keeps fixed-CRF behavior
- `--target-quality <LOW-HIGH>`: CVVDP JOD target range (default `9.35-9.75`). A non-default value forces CVVDP scoring even for SDR <=1080p sources (see Target-Quality Scoring)
- `--crf-range <LOW-HIGH>`: target-quality search bounds (default `4.25-63.75`)
- `--cvvdp-display <PATH>`: optional VSHIP/CVVDP display JSON; otherwise Reel generates a normal-viewing `reel` model. Custom JSON must contain a top-level `reel` model.
- `--metric-workers <N>`: concurrent VSHIP/CUDA scoring workers (default: `4`)
- `--max-probes <N>`: maximum target-quality probes per chunk (default `6`)
- `--crf <VALUE>`: fixed CRF, `1-70` in `0.25` increments. Supplying `--crf` without `--quality-mode` selects `crf` mode for compatibility.
  - Single value: `--crf 26.25` (use for all resolutions)
  - Triple: `--crf 24,26.25,26.5` (SD,HD,UHD)
- `--preset <0-13>`: SVT-AV1 encoder speed/quality (default `6`, lower is slower but higher quality)

**Processing**
- `--disable-autocrop`: Skip black-bar detection and cropping
- `--grain-treatment auto|off`: automatic grain handling in target-quality mode (default `auto`, see Grain Treatment)

**Output**
- `-l, --log-dir <DIR>`: Override the log directory (defaults to `~/.local/state/reel/logs`)
- `-v, --verbose`: Verbose output with detailed status
- `--no-log`: Disable log file creation
- `--keep-workdir`: Keep the `.reel-*` work directory after successful encodes for probe/log analysis

## Parallel Chunked Encoding

Reel splits video into chunks, encodes chunks in parallel with SVT-AV1, merges the encoded chunks, then muxes video with Opus audio, chapters, and metadata. Fixed-CRF mode keeps simple duration-based chunking. Target-quality mode uses shot detection plus target-aware packing to reduce chunk count while keeping CRF decisions visually coherent.

Worker count adapts during encoding: Reel starts conservatively, tests higher concurrency by recent throughput, and backs off on RAM or swap pressure. Target-quality chunks are scheduled in timeline blocks, largest-first within each block, so nearby completed chunks can seed adaptive CRF priors without creating a long-tail of large chunks.

Target-quality mode is enabled in the default build and requires a working `libvship`/CUDA install. Build with `-tags no_vship` to disable target-quality mode entirely and default to fixed-CRF mode.

Interrupted runs can be resumed by running the same command again. Completed chunks are kept in Reel's temporary work directory until the final output is created.

## Target-Quality Scoring

Each probe encodes and scores the **whole chunk** in one metric pass, so the score is exact, and the converged probe is reused verbatim as the final chunk (no re-encode). Planned chunks are capped at 12 seconds. The probe metric depends on the source:

- **HDR and above-1080p sources** search for CVVDP scores in the `9.35-9.75` JOD range (default).
- **SDR sources at or below 1080p** probe with SSIMULACRA2, which scores several times faster on the same GPU. Because SSIMULACRA2 values do not transfer across content (grainy titles score lower at equal perceived quality), each title starts with a short CVVDP warmup: the first ~20 chunks search with CVVDP as usual while every probe is also SSIMULACRA2-scored, the title's offset from the corpus mapping locks, and the remaining chunks search with pure SSIMULACRA2 against the calibrated target (`67.4 + offset`, tolerance `+/- 7.5`, mean-pooled per-frame scores). The offset persists in the work directory, so resumed encodes skip re-calibration.

Passing an explicit `--target-quality` range (other than the built-in default string) or `--cvvdp-display` forces CVVDP scoring for that encode, since both options are CVVDP-denominated.

Verbose output logs each probe's whole-chunk score, the per-chunk probe count, and (for SDR <=1080p) the calibration lock line with the title's offset.

## Grain Treatment

Film grain is expensive to encode: a grainy 4K disc title can cost ten times the bitrate of a clean one at the same quality, and the encoder spends those bits coding noise that is random by nature. In target-quality mode Reel measures each title before encoding by encoding a handful of sample chunks from the middle of the title at a fixed CRF and looking at the bits per pixel they cost. Bits at a fixed CRF are a proxy for grain, not a prediction of what the quality target will spend: a dark grainy film can be cheap at a fixed CRF and still expensive to hold at the target. When the measurement lands in the ambiguous band below the cutoff, Reel re-encodes those same samples to the quality target and decides on what they cost there instead; if that second measurement cannot run, the fixed-CRF verdict stands and the reason is recorded. Titles above the cutoff are encoded from a denoised source with source-matched film grain parameters attached, so the player re-synthesizes grain at decode time instead. Reel estimates grain strength, spatial correlation, and chroma from native-resolution patches in up to 48 original/denoised frame pairs already decoded for the ceiling measurement. The bitrate gate decides treatment, not grain strength; there are no light/medium synthesis tiers. Clean titles, SD sources, and fixed-CRF encodes are never touched, because denoising clean content only costs quality. The verdict, the measurement behind it, and the quality the denoiser itself gives up (the "denoise ceiling", scored against the untouched source) are reported in the log and in the encode statistics, since a treated title's target-quality scores are measured against the denoised source and would otherwise read as better than what is delivered. Pass `--grain-treatment off` to encode every title untreated.

Grain estimation adds bounded startup work, not analysis inside every encode or quality probe. If a treated title cannot produce a reliable model, Reel stops with an error; it does not substitute a generic table or silently encode without synthesis. The exact generated table and its checksum are saved together in `grain-gate.json`, and resumed encodes reuse those bytes without estimating again. Interrupted treated encodes from the former static-table implementation require a new work directory.

## HDR Support

Reel automatically detects and preserves HDR content using native libav probing for color space analysis:
- Detects HDR based on color primaries (BT.2020, BT.2100)
- Recognizes HDR transfer characteristics (PQ, HLG)
- Adapts processing parameters and metadata handling for HDR sources

## Post-Encode Validation

Validation catches mismatches before you archive or publish results:
- **Video codec**: Ensures AV1 output and 10-bit depth
- **Audio codec**: Confirms all audio streams are transcoded to Opus with the expected track count
- **Dimensions**: Validates crop detection and output dimensions
- **Duration**: Compares input and output durations (±1 second tolerance)
- **HDR / Color space**: Uses native libav probing to verify HDR flags and colorimetry
- **Audio sync**: Verifies audio drift is within 100ms tolerance

## Multi-Stream Audio Handling

- Automatically detects every audio stream with libav and transcodes each to Opus with libopusenc
- Encodes audio streams in parallel while video chunks encode
- Preserves the input channel count; surround channels are reordered for Opus mapping, not downmixed
- Bitrate scales with channel count using `128 * (channelEquivalent / 2)^0.75`:
  - Mono: 76 kbps
  - Stereo: 128 kbps
  - 5.1: 258 kbps
  - 7.1: 331 kbps
  - Other layouts: formula-based by channel count

## Progress Reporting

Foreground runs show real-time progress with ETA, fps, and reduction stats. For automation, use the library API with a custom event handler; see `README.md`, `reel.go`, and `events.go`.

## Environment Variables

- `NO_COLOR`: Disable colored output

## Debugging

```bash
# Verbose logging
reel encode -v -i input.mkv -o output/

# Check log files
ls ~/.local/state/reel/logs/
```
