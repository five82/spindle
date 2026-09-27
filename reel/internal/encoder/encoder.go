// Package encoder provides SVT-AV1 library-based encoding for chunked encoding.
package encoder

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/five82/reel/internal/video"
)

// EncConfig contains configuration for encoding a chunk.
type EncConfig struct {
	Inf                *video.Info // Video properties
	CRF                float32     // Quality (CRF value)
	Preset             uint8       // SVT-AV1 preset (0-13)
	Tune               uint8       // SVT-AV1 tune
	Output             string      // Output IVF path
	GrainTable         *string     // EXPERIMENTAL: libaom "filmgrn1" grain table path; attaches FGS params to the bitstream at no encode cost
	Width              uint32      // Frame width (after cropping)
	Height             uint32      // Frame height (after cropping)
	Frames             int         // Number of frames to encode
	LevelOfParallelism uint32      // SVT-AV1 level_of_parallelism (1-6); 0 lets SVT choose

	// Advanced SVT-AV1 parameters
	ACBias                float32
	EnableVarianceBoost   bool
	VarianceBoostStrength uint8
	VarianceOctile        uint8
}

// EncodeChunkToIVF encodes a chunk of video frames to an IVF file using the SVT-AV1 library.
// readFrame is called for each frame to fill the provided buffer with 10-bit YUV420 data.
// progressCb is called periodically with the number of frames encoded so far.
func EncodeChunkToIVF(ctx context.Context, cfg *EncConfig, readFrame func([]byte) error, progressCb func(encoded int)) (err error) {
	if cfg.Frames <= 0 {
		return fmt.Errorf("no frames to encode")
	}

	out, err := os.Create(cfg.Output)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close output file: %w", cerr)
		}
	}()

	enc, err := newSvtEncoder(cfg)
	if err != nil {
		_ = os.Remove(cfg.Output)
		return err
	}
	defer enc.close()

	if err := writeIVFHeader(out, uint16(cfg.Width), uint16(cfg.Height), cfg.Inf.FPSNum, cfg.Inf.FPSDen); err != nil {
		_ = os.Remove(cfg.Output)
		return fmt.Errorf("failed to write IVF header: %w", err)
	}

	frameBuf := make([]byte, video.Calc10BitSize(cfg.Width, cfg.Height))

	writeFrame := func(data []byte, pts int64) error {
		return writeIVFFrame(out, data, pts)
	}

	encoded := 0
	for i := 0; i < cfg.Frames; i++ {
		select {
		case <-ctx.Done():
			_ = os.Remove(cfg.Output)
			return ctx.Err()
		default:
		}

		if err := readFrame(frameBuf); err != nil {
			_ = os.Remove(cfg.Output)
			return fmt.Errorf("failed to read frame %d: %w", i, err)
		}

		if err := enc.sendFrame(frameBuf, int64(i)); err != nil {
			_ = os.Remove(cfg.Output)
			return fmt.Errorf("failed to send frame %d: %w", i, err)
		}

		n, err := enc.drainPackets(writeFrame, false)
		if err != nil {
			_ = os.Remove(cfg.Output)
			return fmt.Errorf("failed to drain packets at frame %d: %w", i, err)
		}
		if n > 0 {
			encoded += n
			if progressCb != nil {
				progressCb(encoded)
			}
		}
	}

	if err := enc.sendEOS(); err != nil {
		_ = os.Remove(cfg.Output)
		return fmt.Errorf("failed to send EOS: %w", err)
	}

	n, err := enc.drainPackets(writeFrame, true)
	if err != nil {
		_ = os.Remove(cfg.Output)
		return fmt.Errorf("failed to drain final packets: %w", err)
	}
	encoded += n
	if progressCb != nil {
		progressCb(encoded)
	}

	// Durable output: every chunk/probe IVF is reusable as final output and
	// relied on for resume, so always flush before returning.
	if err := out.Sync(); err != nil {
		return fmt.Errorf("failed to sync output file: %w", err)
	}

	return nil
}

func chromaSamplePosition(position int32) string {
	switch position {
	case 1:
		return "vertical"
	case 2:
		return "colocated"
	default:
		return "unknown"
	}
}

// SvtParamsDisplay returns a human-readable colon-separated string of key SVT-AV1 parameters
// for display purposes (similar to FFmpeg's -svtav1-params format).
func SvtParamsDisplay(acBias float32, enableVarianceBoost bool, tune uint8) string {
	params := []string{
		fmt.Sprintf("ac-bias=%g", acBias),
	}

	if enableVarianceBoost {
		params = append(params, "enable-variance-boost=1")
	} else {
		params = append(params, "enable-variance-boost=0")
	}

	params = append(params,
		fmt.Sprintf("tune=%d", tune),
		"keyint=10s",
		"scd=0",
		"scm=0",
		fmt.Sprintf("level=%d.%d", signaledLevel/10, signaledLevel%10),
		fmt.Sprintf("mbr=%d", maxBitRateBps),
	)

	return strings.Join(params, ":")
}
