//go:build cgo && !no_vship

package quality

/*
#cgo LDFLAGS: -lvship
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include "vship_colorspace.h"

typedef struct { unsigned int id; } VshipSSIMU2Handler;

extern Vship_Exception Vship_SetDevice(int gpu_id);
extern Vship_Exception Vship_SSIMU2Init(VshipSSIMU2Handler* handler, Vship_Colorspace_t src_colorspace, Vship_Colorspace_t dis_colorspace);
extern Vship_Exception Vship_SSIMU2Free(VshipSSIMU2Handler handler);
extern Vship_Exception Vship_ComputeSSIMU2(VshipSSIMU2Handler handler, double* score, const uint8_t* srcp1[3], const uint8_t* srcp2[3], const int64_t lineSize[3], const int64_t lineSize2[3]);
extern int Vship_SSIMU2GetDetailedLastError(VshipSSIMU2Handler handler, char* out_message, int len);

static Vship_Exception reel_vship_compute_ssimu2(
	VshipSSIMU2Handler handler,
	double* score,
	const uint8_t* src0,
	const uint8_t* src1,
	const uint8_t* src2,
	const uint8_t* dist0,
	const uint8_t* dist1,
	const uint8_t* dist2,
	int64_t src0_stride,
	int64_t src1_stride,
	int64_t src2_stride,
	int64_t dist0_stride,
	int64_t dist1_stride,
	int64_t dist2_stride
) {
	const uint8_t* srcp1[3] = {src0, src1, src2};
	const uint8_t* srcp2[3] = {dist0, dist1, dist2};
	const int64_t lineSize[3] = {src0_stride, src1_stride, src2_stride};
	const int64_t lineSize2[3] = {dist0_stride, dist1_stride, dist2_stride};
	return Vship_ComputeSSIMU2(handler, score, srcp1, srcp2, lineSize, lineSize2);
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/five82/reel/internal/video"
)

// SSIMU2Processor is a SSIMU2 scorer backed by VSHIP. Unlike CVVDP, SSIMU2 is
// stateless per frame pair, so there is no Reset method.
type SSIMU2Processor struct {
	handler C.VshipSSIMU2Handler
	closed  bool
}

func NewSSIMU2Processor(width, height uint32, inf *video.Info) (*SSIMU2Processor, error) {
	vshipDeviceOnce.Do(func() {
		if ret := C.Vship_SetDevice(0); ret != 0 {
			vshipDeviceOnce.err = fmt.Errorf("Vship_SetDevice failed: %s", vshipLastError())
		}
	})
	if vshipDeviceOnce.err != nil {
		return nil, vshipDeviceOnce.err
	}

	if inf == nil {
		return nil, fmt.Errorf("nil video info")
	}
	srcColorspace := createYUVColorspace(width, height, inf)
	disColorspace := createYUVColorspace(width, height, inf)

	var handler C.VshipSSIMU2Handler
	ret := C.Vship_SSIMU2Init(&handler, srcColorspace, disColorspace)
	if ret != 0 {
		return nil, fmt.Errorf("Vship_SSIMU2Init failed: %s", vshipLastError())
	}
	return &SSIMU2Processor{handler: handler}, nil
}

func (p *SSIMU2Processor) Close() error {
	if p == nil || p.closed {
		return nil
	}
	p.closed = true
	ret := C.Vship_SSIMU2Free(p.handler)
	if ret != 0 {
		return fmt.Errorf("Vship_SSIMU2Free failed: %s", vshipLastError())
	}
	return nil
}

func (p *SSIMU2Processor) ComputeSSIMU2(src, dist FramePlanes) (float64, error) {
	if p == nil || p.closed {
		return 0, fmt.Errorf("SSIMU2 handler is closed")
	}
	var score C.double
	ret := C.reel_vship_compute_ssimu2(
		p.handler,
		&score,
		(*C.uint8_t)(unsafe.Pointer(src.Planes[0])),
		(*C.uint8_t)(unsafe.Pointer(src.Planes[1])),
		(*C.uint8_t)(unsafe.Pointer(src.Planes[2])),
		(*C.uint8_t)(unsafe.Pointer(dist.Planes[0])),
		(*C.uint8_t)(unsafe.Pointer(dist.Planes[1])),
		(*C.uint8_t)(unsafe.Pointer(dist.Planes[2])),
		C.int64_t(src.Strides[0]),
		C.int64_t(src.Strides[1]),
		C.int64_t(src.Strides[2]),
		C.int64_t(dist.Strides[0]),
		C.int64_t(dist.Strides[1]),
		C.int64_t(dist.Strides[2]),
	)
	if ret != 0 {
		return 0, fmt.Errorf("Vship_ComputeSSIMU2 failed: %s", vshipLastError())
	}
	return float64(score), nil
}
