//go:build cgo && !no_vship

package quality

import "testing"

func TestClosedSSIMU2Processor(t *testing.T) {
	var nilProcessor *SSIMU2Processor
	if err := nilProcessor.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := nilProcessor.ComputeSSIMU2(FramePlanes{}, FramePlanes{}); err == nil {
		t.Fatal("nil processor must reject scoring")
	}
	closed := &SSIMU2Processor{closed: true}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closed.ComputeSSIMU2(FramePlanes{}, FramePlanes{}); err == nil {
		t.Fatal("closed processor must reject scoring")
	}
}
