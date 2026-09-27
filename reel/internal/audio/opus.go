package audio

/*
#include <stdlib.h>

typedef struct OggOpusComments OggOpusComments;
typedef struct OggOpusEnc OggOpusEnc;

int reel_ope_load(char *errbuf, size_t errlen);
OggOpusComments* reel_ope_comments_create_call(void);
void reel_ope_comments_destroy_call(OggOpusComments *comments);
OggOpusEnc* reel_ope_encoder_create_file_call(const char *path, OggOpusComments *comments, int rate, int channels, int family, int *error);
int reel_ope_encoder_write_float_call(OggOpusEnc *enc, const float *pcm, int samples_per_channel);
int reel_ope_encoder_drain_call(OggOpusEnc *enc);
void reel_ope_encoder_destroy_call(OggOpusEnc *enc);
int reel_ope_encoder_ctl_int(OggOpusEnc *enc, int request, int value);
const char* reel_ope_strerror_call(int error);

enum {
	REEL_OPUS_SET_APPLICATION_REQUEST = 4000,
	REEL_OPUS_SET_BITRATE_REQUEST = 4002,
	REEL_OPUS_SET_VBR_REQUEST = 4006,
	REEL_OPUS_SET_MAX_BANDWIDTH_REQUEST = 4004,
	REEL_OPUS_SET_COMPLEXITY_REQUEST = 4010,
	REEL_OPUS_SET_VBR_CONSTRAINT_REQUEST = 4020,
	REEL_OPUS_APPLICATION_AUDIO = 2049,
	REEL_OPUS_BANDWIDTH_FULLBAND = 1105,
	REEL_OPUS_FAMILY_MONO_STEREO = 0,
	REEL_OPUS_FAMILY_SURROUND = 1,
};
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

type opusEncoder struct {
	ptr *C.OggOpusEnc
}

var (
	opusLoadOnce sync.Once
	opusLoadErr  error
)

func loadOpusenc() error {
	opusLoadOnce.Do(func() {
		var errbuf [512]C.char
		if C.reel_ope_load(&errbuf[0], C.size_t(len(errbuf))) < 0 {
			opusLoadErr = errors.New(C.GoString(&errbuf[0]))
		}
	})
	return opusLoadErr
}

func newOpusEncoder(path string, channels, bitrate uint32) (*opusEncoder, error) {
	if err := loadOpusenc(); err != nil {
		return nil, err
	}

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	comments := C.reel_ope_comments_create_call()
	if comments == nil {
		return nil, fmt.Errorf("failed to create Opus comments")
	}
	defer C.reel_ope_comments_destroy_call(comments)

	family := C.int(C.REEL_OPUS_FAMILY_SURROUND)
	if channels <= 2 {
		family = C.int(C.REEL_OPUS_FAMILY_MONO_STEREO)
	}

	var opusErr C.int
	ptr := C.reel_ope_encoder_create_file_call(cPath, comments, outputSampleRate, C.int(channels), family, &opusErr)
	if ptr == nil {
		return nil, fmt.Errorf("opus encoder failed: %s", opusError(opusErr))
	}

	enc := &opusEncoder{ptr: ptr}
	settings := []struct {
		request C.int
		value   C.int
	}{
		{C.REEL_OPUS_SET_BITRATE_REQUEST, C.int(bitrate * 1000)},
		{C.REEL_OPUS_SET_VBR_REQUEST, 1},
		{C.REEL_OPUS_SET_VBR_CONSTRAINT_REQUEST, 0},
		{C.REEL_OPUS_SET_COMPLEXITY_REQUEST, 10},
		{C.REEL_OPUS_SET_MAX_BANDWIDTH_REQUEST, C.REEL_OPUS_BANDWIDTH_FULLBAND},
		{C.REEL_OPUS_SET_APPLICATION_REQUEST, C.REEL_OPUS_APPLICATION_AUDIO},
	}
	for _, setting := range settings {
		if ret := C.reel_ope_encoder_ctl_int(ptr, setting.request, setting.value); ret != 0 {
			enc.destroy()
			return nil, fmt.Errorf("opus encoder setting failed: %s", opusError(ret))
		}
	}

	return enc, nil
}

func (e *opusEncoder) writeFloat(pcm []float32, channels int) error {
	if len(pcm) == 0 {
		return nil
	}
	ret := C.reel_ope_encoder_write_float_call(e.ptr, (*C.float)(unsafe.Pointer(&pcm[0])), C.int(len(pcm)/channels))
	if ret != 0 {
		return fmt.Errorf("opus write failed: %s", opusError(ret))
	}
	return nil
}

func (e *opusEncoder) close() error {
	if e == nil || e.ptr == nil {
		return nil
	}
	ret := C.reel_ope_encoder_drain_call(e.ptr)
	e.destroy()
	if ret != 0 {
		return fmt.Errorf("opus drain failed: %s", opusError(ret))
	}
	return nil
}

func (e *opusEncoder) destroy() {
	if e == nil || e.ptr == nil {
		return
	}
	C.reel_ope_encoder_destroy_call(e.ptr)
	e.ptr = nil
}

func opusError(code C.int) string {
	msg := C.reel_ope_strerror_call(code)
	if msg == nil {
		return fmt.Sprintf("error %d", int(code))
	}
	return C.GoString(msg)
}
