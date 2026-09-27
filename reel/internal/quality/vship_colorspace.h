/* Shared Vship colorspace types used by both vship_cgo.go (CVVDP) and
 * ssimu2_cgo.go (SSIMU2). cgo does not unify identically-named C typedefs
 * declared separately in each file's preamble comment -- it reports
 * "inconsistent definitions" even when the text is byte-identical. Both
 * files must #include this single header so the Go-level createYUVColorspace
 * helper in vship_cgo.go can be reused from ssimu2_cgo.go.
 */
#ifndef REEL_VSHIP_COLORSPACE_H
#define REEL_VSHIP_COLORSPACE_H

#include <stdbool.h>
#include <stdint.h>

typedef enum {
	Vship_SampleFLOAT = 0,
	Vship_SampleHALF = 1,
	Vship_SampleUINT8 = 2,
	Vship_SampleUINT9 = 3,
	Vship_SampleUINT10 = 5,
	Vship_SampleUINT12 = 7,
	Vship_SampleUINT14 = 9,
	Vship_SampleUINT16 = 11,
} Vship_Sample;

typedef enum { Vship_RangeLimited = 0, Vship_RangeFull = 1 } Vship_Range;
typedef struct { int subw; int subh; } Vship_ChromaSubsample;
typedef enum { Vship_ChromaLeft = 0, Vship_ChromaCenter = 1, Vship_ChromaTopLeft = 2, Vship_ChromaTop = 3 } Vship_ChromaLocation;
typedef enum { Vship_ColorYUV = 0, Vship_ColorRGB = 1 } Vship_ColorFamily;
typedef enum {
	Vship_MATRIX_RGB = 0,
	Vship_MATRIX_BT709 = 1,
	Vship_MATRIX_BT470BG = 5,
	Vship_MATRIX_ST170M = 6,
	Vship_MATRIX_BT2020_NCL = 9,
	Vship_MATRIX_BT2020_CL = 10,
	Vship_MATRIX_BT2100_ICTCP = 14,
} Vship_YUVMatrix;
typedef enum {
	Vship_TRC_BT709 = 1,
	Vship_TRC_BT470M = 4,
	Vship_TRC_BT470BG = 5,
	Vship_TRC_BT601 = 6,
	Vship_TRC_LINEAR = 8,
	Vship_TRC_SRGB = 13,
	Vship_TRC_PQ = 16,
	Vship_TRC_ST428 = 17,
	Vship_TRC_HLG = 18,
} Vship_TransferFunction;
typedef enum {
	Vship_PRIMARIES_INTERNAL = -1,
	Vship_PRIMARIES_BT709 = 1,
	Vship_PRIMARIES_BT470M = 4,
	Vship_PRIMARIES_BT470BG = 5,
	Vship_PRIMARIES_BT2020 = 9,
} Vship_Primaries;
typedef struct { int top; int bottom; int left; int right; } Vship_CropRectangle;
typedef struct {
	int64_t width;
	int64_t height;
	int64_t target_width;
	int64_t target_height;
	Vship_Sample sample;
	Vship_Range range_;
	Vship_ChromaSubsample subsampling;
	Vship_ChromaLocation chroma_location;
	Vship_ColorFamily colorFamily;
	Vship_YUVMatrix YUVMatrix;
	Vship_TransferFunction transferFunction;
	Vship_Primaries primaries;
	Vship_CropRectangle crop;
} Vship_Colorspace_t;

typedef int Vship_Exception;

#endif /* REEL_VSHIP_COLORSPACE_H */
