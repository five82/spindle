//go:build no_vship || !cgo

package quality

import (
	"errors"

	"github.com/five82/reel/internal/video"
)

// VshipProcessor is a CVVDP scorer backed by VSHIP. This stub is used for
// builds that explicitly disable VSHIP with -tags no_vship, or when cgo is off.
type VshipProcessor struct{}

var errVshipUnavailable = errors.New("VSHIP support is not enabled in this build; rebuild Reel without -tags no_vship and with libvship installed")

func VshipBuildEnabled() bool { return false }

func newMetricBuffer(_ int) ([]byte, func(), error) {
	return nil, nil, errVshipUnavailable
}

func NewVshipProcessor(_, _ uint32, _ *video.Info, _ string) (*VshipProcessor, error) {
	return nil, errVshipUnavailable
}

func (p *VshipProcessor) Close() error { return nil }

func (p *VshipProcessor) ResetCVVDP() error {
	return errVshipUnavailable
}

func (p *VshipProcessor) ComputeCVVDP(_, _ FramePlanes) (float32, error) {
	return 0, errVshipUnavailable
}

// SSIMU2Processor is a SSIMU2 scorer backed by VSHIP. This stub is used for
// builds that explicitly disable VSHIP with -tags no_vship, or when cgo is off.
type SSIMU2Processor struct{}

func NewSSIMU2Processor(_, _ uint32, _ *video.Info) (*SSIMU2Processor, error) {
	return nil, errVshipUnavailable
}

func (p *SSIMU2Processor) Close() error { return nil }

func (p *SSIMU2Processor) ComputeSSIMU2(_, _ FramePlanes) (float64, error) {
	return 0, errVshipUnavailable
}
