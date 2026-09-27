# reel

Reel is an AV1 encoding tool for home-streaming libraries, with opinionated defaults and quality feedback instead of manual encoder tuning. It uses SVT-AV1 and FFmpeg/libav for parallel chunked encoding and is available as a CLI and a Go library.

Reel prioritizes throughput and consistent viewing quality over archival fidelity. Target-quality mode aims for more consistent quality across varied content than fixed CRF, accepting quality tradeoffs that are invisible at normal viewing distances rather than spending more compute chasing near-optimal per-scene quality.

## Project status

Reel is an experimental personal project, shared as is. It is built around my own workflow, hardware, and preferences; behavior and APIs may change or break. Support is best-effort, and questions may go unanswered. For other parallel chunked encoding tools, consider [av1an](https://github.com/rust-av/Av1an) or [xav](https://github.com/emrakyz/xav).

## Features

- Parallel encoding with automatic worker adjustment
- Target-quality encoding by default, with fixed CRF available
- Automatic black-bar crop detection
- [Automatic grain treatment](docs/USAGE.md#grain-treatment) in target-quality mode
- HDR10/HLG metadata preservation
- Multi-track audio transcoding to Opus
- Resume interrupted encodes from completed chunks
- Post-encode validation of codecs, dimensions, duration, audio sync, and HDR
- Go library API for embedding

## Installation

Reel is developed and tested on Linux. Both build options below require native libraries and cgo; the default build also requires an NVIDIA GPU compatible with your CUDA installation.

### Common dependencies

- Go 1.27.1+
- A C compiler and `pkg-config`, with cgo enabled
- SVT-AV1 development headers and shared library (`libSvtAv1Enc`); automatic grain treatment requires version 2.3.0 or newer
- `libopusenc` shared library for Opus audio encoding
- FFmpeg/libav development libraries: `libavformat`, `libavcodec`, `libavutil`, `libavfilter`, `libswscale`, and `libswresample`

On Ubuntu/Debian, install the native dependencies with:

```bash
sudo apt-get install build-essential pkg-config \
  libavformat-dev libavcodec-dev libavutil-dev libavfilter-dev \
  libswscale-dev libswresample-dev libopusenc0 libsvtav1enc-dev
```

Package versions vary by distribution release; older releases may need a newer SVT-AV1 installation. Install Go separately. The command above does not install VSHIP or CUDA.

Choose one of the following builds.

### Default target-quality build

Requires [VSHIP](https://codeberg.org/Line-fr/Vship) (`libvship`) built with the CUDA backend, plus a working CUDA installation and compatible NVIDIA GPU.

**Build VSHIP with `MITIGATE_MALLOC_ASYNC=on` to avoid silently corrupted quality scores during concurrent scoring:**

```bash
# In the VSHIP source checkout
make build BACKEND=Cuda MITIGATE_MALLOC_ASYNC=on
```

Follow VSHIP's installation instructions to install the library. See the [concurrency bug notes](docs/VSHIP_CONCURRENCY_BUG.md) for the reason this flag is required.

Then install Reel:

```bash
go install -trimpath github.com/five82/reel/cmd/reel@latest
```

### Fixed-CRF-only build

Requires only the common dependencies above, without VSHIP or CUDA. Target-quality mode is unavailable in this build.

```bash
go install -trimpath -tags no_vship github.com/five82/reel/cmd/reel@latest
```

### Building from source

With the dependencies for your chosen build installed:

```bash
git clone https://github.com/five82/reel
cd reel
go build -trimpath -o reel ./cmd/reel
```

For a fixed-CRF-only build, replace the last command with `go build -trimpath -tags no_vship -o reel ./cmd/reel`.

## Usage

```bash
# Encode a single file
reel encode -i input.mkv -o output/

# Encode a directory
reel encode -i /videos/ -o /encoded/

# Use fixed CRF instead of target-quality mode
reel encode -i input.mkv -o output/ --quality-mode crf --crf 26.25
```

Reel encodes video chunks in parallel, then merges them and muxes Opus audio, chapters, and metadata. The default build adjusts CRF using measured quality; the `no_vship` build defaults to fixed CRF.

If a run is interrupted, run the same command again to resume from completed chunks.

Run `reel encode --help` for the full flag list. The [usage guide](docs/USAGE.md) covers quality scoring, grain treatment, HDR, audio handling, and troubleshooting.

## Go library

The library uses the same native dependencies and build tags as the CLI:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/five82/reel"
)

func main() {
    encoder, err := reel.New()
    if err != nil {
        log.Fatal(err)
    }

    result, err := encoder.Encode(context.Background(), "input.mkv", "output/", nil)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Encoded: %s, reduction: %.1f%%\n",
        result.OutputFile, result.SizeReductionPercent)
}
```

Use `reel.New(reel.WithCRF(26.25))` to select fixed-CRF mode explicitly. Pass an event handler instead of `nil` to receive progress and completion events; see the [API documentation](https://pkg.go.dev/github.com/five82/reel).

## Development

Run the full local CI check before handing off changes:

```bash
./check-ci.sh
```

To install a source checkout over the `reel` on `PATH`, use `./deploy.sh` after the checks pass. Deployment builds with VSHIP support.

## Credits and license

Thanks to [xav](https://github.com/emrakyz/xav) for the libav-based parallel chunked encoding approach.

Reel is licensed under [GPLv3](LICENSE).
