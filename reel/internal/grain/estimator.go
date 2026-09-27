// Package grain estimates decoder-side AV1 grain from bounded samples of the
// original and the actual encoder denoiser output. It does not decode or filter.
package grain

/*
#cgo LDFLAGS: -lm
#include "estimator.h"
*/
import "C"

import (
	"fmt"
	"strings"
	"time"
	"unsafe"
)

// Version identifies the sampling, fitting, and quantization policy. Saved
// models are replayed verbatim, never re-fitted under a newer policy on resume.
const Version = "aom-patches-v1"

// FramesPerChunk bounds analysis independently of chunk duration. Twelve gate
// chunks give at most 48 frames. Native patches preserve spatial correlation;
// reducing resolution to make estimation cheaper would change the grain model.
// The 48-frame synthetic title fit measured about 0.47s on the 7950X; actual
// frame coverage and <1% added feature wall still need real-title validation.
// See docs/PERFORMANCE_TESTING.md, "Sampled source-matched grain".
const FramesPerChunk = 4

// SampleFrame picks the interior of each quarter, avoiding filter startup and
// the last frame. i is chunk-relative; frames is the whole chunk length.
func SampleFrame(i, frames int) bool {
	if i < 0 || i >= frames {
		return false
	}
	for n := range FramesPerChunk {
		if i == (2*n+1)*frames/(2*FramesPerChunk) {
			return true
		}
	}
	return false
}

// Estimate contains a single filmgrn1 entry and evidence for its provenance.
// Seconds is sampler plus fitter wall time, not the enclosing decode/score pass.
type Estimate struct {
	Table          string
	Frames         []int
	AcceptedFrames int
	Patches        int
	Seconds        float64
}

// Estimator is owned by one sequential sample pass. Observe copies only the
// selected patches; it never retains borrowed frame buffers. Close is required.
type Estimator struct {
	ptr       *C.reel_grain_estimator
	width     int
	height    int
	estimate  Estimate
	lastFrame int
}

func New(width, height int) (*Estimator, error) {
	// Bound the C int plane offsets as well as rejecting invalid 4:2:0 geometry.
	if width < 32 || height < 32 || width > 8192 || height > 8192 || width%2 != 0 || height%2 != 0 {
		return nil, fmt.Errorf("grain estimation requires even dimensions between 32 and 8192, got %dx%d", width, height)
	}
	ptr := C.reel_grain_create()
	if ptr == nil {
		return nil, fmt.Errorf("failed to allocate grain estimator")
	}
	return &Estimator{ptr: ptr, width: width, height: height, lastFrame: -1}, nil
}

func (e *Estimator) Close() {
	if e.ptr != nil {
		C.reel_grain_free(e.ptr)
		e.ptr = nil
	}
}

// Observe accepts packed YUV420P10LE, including 8-bit sources promoted to
// 10-bit by Reel's frame reader. Subtraction happens before any quantization.
func (e *Estimator) Observe(frame int, source, denoised []byte) error {
	if e.ptr == nil {
		return fmt.Errorf("grain estimator is closed")
	}
	if frame <= e.lastFrame {
		return fmt.Errorf("grain sample frames must be increasing: %d after %d", frame, e.lastFrame)
	}
	if len(e.estimate.Frames) >= int(C.REEL_GRAIN_MAX_FRAMES) {
		return fmt.Errorf("grain estimation exceeded its %d-frame budget", int(C.REEL_GRAIN_MAX_FRAMES))
	}
	required := e.width * e.height * 3
	if len(source) != required || len(denoised) != required {
		return fmt.Errorf("grain estimation needs aligned %dx%d YUV420P10LE buffers (%d bytes)", e.width, e.height, required)
	}
	start := time.Now()
	count := int(C.reel_grain_sample(e.ptr, (*C.uint8_t)(unsafe.Pointer(&source[0])),
		(*C.uint8_t)(unsafe.Pointer(&denoised[0])), C.int(e.width), C.int(e.height), C.int(len(e.estimate.Frames))))
	e.estimate.Seconds += time.Since(start).Seconds()
	if count < 0 {
		return fmt.Errorf("grain sampling failed at frame %d", frame)
	}
	e.lastFrame = frame
	e.estimate.Frames = append(e.estimate.Frames, frame)
	e.estimate.Patches += count
	if count > 0 {
		e.estimate.AcceptedFrames++
	}
	return nil
}

func (e *Estimator) Finish() (Estimate, error) {
	if e.ptr == nil {
		return Estimate{}, fmt.Errorf("grain estimator is closed")
	}
	// Do not extrapolate an entire title from one fortunate frame. Equal frame
	// quotas also keep large flat scenes from overwhelming the other samples.
	if e.estimate.Patches < 32 || e.estimate.AcceptedFrames*2 < len(e.estimate.Frames) {
		return Estimate{}, fmt.Errorf("insufficient grain samples: %d patches in %d of %d frames; need at least 32 patches and half the sampled frames", e.estimate.Patches, e.estimate.AcceptedFrames, len(e.estimate.Frames))
	}
	start := time.Now()
	var params C.reel_aom_film_grain_t
	ok := C.reel_grain_fit(e.ptr, &params)
	e.estimate.Seconds += time.Since(start).Seconds()
	if ok == 0 {
		return Estimate{}, fmt.Errorf("grain model could not be fitted reliably (degenerate residual, unrepresentable strength, or unstable AR coefficients)")
	}
	result := e.estimate
	result.Frames = append([]int(nil), result.Frames...)
	result.Table = grainTable(&params)
	return result, nil
}

func grainTable(p *C.reel_aom_film_grain_t) string {
	var out strings.Builder
	fmt.Fprintf(&out, "filmgrn1\nE 0 9223372036854775807 1 %d 1\n", int(p.random_seed))
	fmt.Fprintf(&out, "\tp %d %d %d %d %d %d %d %d %d %d %d %d\n",
		int(p.ar_coeff_lag), int(p.ar_coeff_shift), int(p.grain_scale_shift), int(p.scaling_shift),
		int(p.chroma_scaling_from_luma), int(p.overlap_flag),
		int(p.cb_mult), int(p.cb_luma_mult), int(p.cb_offset), int(p.cr_mult), int(p.cr_luma_mult), int(p.cr_offset))
	writePoints := func(name string, points [][2]C.int, count int) {
		fmt.Fprintf(&out, "\t%s %d", name, count)
		for _, point := range points[:count] {
			fmt.Fprintf(&out, " %d %d", int(point[0]), int(point[1]))
		}
		out.WriteByte('\n')
	}
	writePoints("sY", p.scaling_points_y[:], int(p.num_y_points))
	writePoints("sCb", p.scaling_points_cb[:], int(p.num_cb_points))
	writePoints("sCr", p.scaling_points_cr[:], int(p.num_cr_points))
	writeCoeffs := func(name string, coeffs []C.int, count int) {
		fmt.Fprintf(&out, "\t%s", name)
		for _, v := range coeffs[:count] {
			fmt.Fprintf(&out, " %d", int(v))
		}
		out.WriteByte('\n')
	}
	n := 2 * int(p.ar_coeff_lag) * (int(p.ar_coeff_lag) + 1)
	writeCoeffs("cY", p.ar_coeffs_y[:], n)
	writeCoeffs("cCb", p.ar_coeffs_cb[:], n+1)
	writeCoeffs("cCr", p.ar_coeffs_cr[:], n+1)
	return out.String()
}
