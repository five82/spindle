// Package audio provides native libav decoding and libopusenc audio encoding.
package audio

/*
#cgo pkg-config: libavformat libavcodec libavutil libswresample
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <errno.h>
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/channel_layout.h>
#include <libavutil/error.h>
#include <libavutil/frame.h>
#include <libavutil/log.h>
#include <libavutil/samplefmt.h>
#include <libswresample/swresample.h>
#include <stdlib.h>
#include <string.h>

// libopusenc is loaded dynamically so Reel can still build on systems where the
// optional runtime audio encoder library is not installed.
typedef struct OggOpusComments OggOpusComments;
typedef struct OggOpusEnc OggOpusEnc;

typedef OggOpusComments* (*ope_comments_create_fn)(void);
typedef void (*ope_comments_destroy_fn)(OggOpusComments *comments);
typedef OggOpusEnc* (*ope_encoder_create_file_fn)(const char *path, OggOpusComments *comments, int rate, int channels, int family, int *error);
typedef int (*ope_encoder_write_float_fn)(OggOpusEnc *enc, const float *pcm, int samples_per_channel);
typedef int (*ope_encoder_drain_fn)(OggOpusEnc *enc);
typedef void (*ope_encoder_destroy_fn)(OggOpusEnc *enc);
typedef int (*ope_encoder_ctl_fn)(OggOpusEnc *enc, int request, ...);
typedef const char* (*ope_strerror_fn)(int error);

static void *reel_ope_handle = NULL;
static ope_comments_create_fn reel_ope_comments_create = NULL;
static ope_comments_destroy_fn reel_ope_comments_destroy = NULL;
static ope_encoder_create_file_fn reel_ope_encoder_create_file = NULL;
static ope_encoder_write_float_fn reel_ope_encoder_write_float = NULL;
static ope_encoder_drain_fn reel_ope_encoder_drain = NULL;
static ope_encoder_destroy_fn reel_ope_encoder_destroy = NULL;
static ope_encoder_ctl_fn reel_ope_encoder_ctl = NULL;
static ope_strerror_fn reel_ope_strerror = NULL;

static int reel_load_sym(void **target, const char *name, char *errbuf, size_t errlen) {
	*target = dlsym(reel_ope_handle, name);
	if (*target == NULL) {
		snprintf(errbuf, errlen, "libopusenc missing symbol %s", name);
		return -1;
	}
	return 0;
}

int reel_ope_load(char *errbuf, size_t errlen) {
	if (reel_ope_handle != NULL) {
		return 0;
	}

	reel_ope_handle = dlopen("libopusenc.so.0", RTLD_LAZY);
	if (reel_ope_handle == NULL) {
		reel_ope_handle = dlopen("libopusenc.so", RTLD_LAZY);
	}
	if (reel_ope_handle == NULL) {
		const char *err = dlerror();
		snprintf(errbuf, errlen, "libopusenc not found: %s", err == NULL ? "unknown error" : err);
		return -1;
	}

	if (reel_load_sym((void **)&reel_ope_comments_create, "ope_comments_create", errbuf, errlen) < 0 ||
		reel_load_sym((void **)&reel_ope_comments_destroy, "ope_comments_destroy", errbuf, errlen) < 0 ||
		reel_load_sym((void **)&reel_ope_encoder_create_file, "ope_encoder_create_file", errbuf, errlen) < 0 ||
		reel_load_sym((void **)&reel_ope_encoder_write_float, "ope_encoder_write_float", errbuf, errlen) < 0 ||
		reel_load_sym((void **)&reel_ope_encoder_drain, "ope_encoder_drain", errbuf, errlen) < 0 ||
		reel_load_sym((void **)&reel_ope_encoder_destroy, "ope_encoder_destroy", errbuf, errlen) < 0 ||
		reel_load_sym((void **)&reel_ope_encoder_ctl, "ope_encoder_ctl", errbuf, errlen) < 0 ||
		reel_load_sym((void **)&reel_ope_strerror, "ope_strerror", errbuf, errlen) < 0) {
		return -1;
	}
	return 0;
}

static const int REEL_OPUS_SET_APPLICATION_REQUEST = 4000;
static const int REEL_OPUS_SET_BITRATE_REQUEST = 4002;
static const int REEL_OPUS_SET_VBR_REQUEST = 4006;
static const int REEL_OPUS_SET_MAX_BANDWIDTH_REQUEST = 4004;
static const int REEL_OPUS_SET_COMPLEXITY_REQUEST = 4010;
static const int REEL_OPUS_SET_VBR_CONSTRAINT_REQUEST = 4020;
static const int REEL_OPUS_APPLICATION_AUDIO = 2049;
static const int REEL_OPUS_BANDWIDTH_FULLBAND = 1105;
static const int REEL_OPUS_FAMILY_MONO_STEREO = 0;
static const int REEL_OPUS_FAMILY_SURROUND = 1;

OggOpusComments* reel_ope_comments_create_call(void) { return reel_ope_comments_create(); }
void reel_ope_comments_destroy_call(OggOpusComments *comments) { reel_ope_comments_destroy(comments); }
OggOpusEnc* reel_ope_encoder_create_file_call(const char *path, OggOpusComments *comments, int rate, int channels, int family, int *error) {
	return reel_ope_encoder_create_file(path, comments, rate, channels, family, error);
}
int reel_ope_encoder_write_float_call(OggOpusEnc *enc, const float *pcm, int samples_per_channel) {
	return reel_ope_encoder_write_float(enc, pcm, samples_per_channel);
}
int reel_ope_encoder_drain_call(OggOpusEnc *enc) { return reel_ope_encoder_drain(enc); }
void reel_ope_encoder_destroy_call(OggOpusEnc *enc) { reel_ope_encoder_destroy(enc); }
int reel_ope_encoder_ctl_int(OggOpusEnc *enc, int request, int value) { return reel_ope_encoder_ctl(enc, request, value); }
const char* reel_ope_strerror_call(int error) { return reel_ope_strerror(error); }

static void reel_audio_init_av_log(void) {
	av_log_set_level(AV_LOG_ERROR);
}

AVStream* reel_audio_stream_at(AVFormatContext *ctx, int idx) {
	return ctx->streams[idx];
}

int reel_audio_find_stream_pos(AVFormatContext *ctx, int stream_index) {
	for (unsigned int i = 0; i < ctx->nb_streams; i++) {
		if (ctx->streams[i]->index == stream_index) {
			return (int)i;
		}
	}
	return -1;
}

void reel_audio_discard_except(AVFormatContext *ctx, int stream_pos) {
	for (unsigned int i = 0; i < ctx->nb_streams; i++) {
		AVStream *stream = ctx->streams[i];
		stream->discard = ((int)i == stream_pos) ? AVDISCARD_DEFAULT : AVDISCARD_ALL;
	}
}

int reel_audio_channels(const AVCodecParameters *par) {
	return par->ch_layout.nb_channels;
}

int reel_swr_alloc_for_stream(struct SwrContext **swr, const AVChannelLayout *layout, int in_fmt, int in_rate) {
	return swr_alloc_set_opts2(swr, layout, AV_SAMPLE_FMT_FLT, 48000, layout, in_fmt, in_rate, 0, NULL);
}

int reel_swr_convert_frame(struct SwrContext *swr, float *out, int out_count, const AVFrame *frame) {
	uint8_t *out_data = (uint8_t *)out;
	return swr_convert(swr, &out_data, out_count, (const uint8_t **)frame->extended_data, frame->nb_samples);
}

int reel_swr_flush(struct SwrContext *swr, float *out, int out_count) {
	uint8_t *out_data = (uint8_t *)out;
	return swr_convert(swr, &out_data, out_count, NULL, 0);
}

int reel_audio_averror_eagain(void) {
	return AVERROR(EAGAIN);
}

int reel_audio_averror_eof(void) {
	return AVERROR_EOF;
}

void reel_audio_strerror(int errnum, char *buf, size_t buflen) {
	av_strerror(errnum, buf, buflen);
}
*/
import "C"

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"

	"github.com/five82/reel/internal/media"
)

const outputSampleRate = 48000

// CalculateBitrate returns the Opus audio bitrate in kbps based on channel count.
func CalculateBitrate(channels uint32) uint32 {
	if channels == 0 {
		return 0
	}
	return uint32(128 * math.Pow(channelEquivalent(channels)/2, 0.75))
}

func channelEquivalent(channels uint32) float64 {
	switch channels {
	case 1, 2:
		return float64(channels)
	case 3:
		return 2.1
	case 4:
		return 3.1
	case 5:
		return 4.1
	case 6:
		return 5.1
	case 7:
		return 6.1
	case 8:
		return 7.1
	default:
		return float64(channels)
	}
}

var initOnce sync.Once

func initLibav() {
	initOnce.Do(func() {
		C.reel_audio_init_av_log()
	})
}

// EncodedStream is a native Opus output for one source audio stream.
type EncodedStream struct {
	Info media.AudioStreamInfo
	Path string
}

// EncodeStreams encodes all given streams to Opus in parallel.
func EncodeStreams(ctx context.Context, inputPath, workDir string, streams []media.AudioStreamInfo, videoDurationSecs float64) ([]EncodedStream, error) {
	if len(streams) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]EncodedStream, len(streams))
	errCh := make(chan error, len(streams))
	var wg sync.WaitGroup

	for i, stream := range streams {
		i, stream := i, stream
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := AudioPath(workDir, i)
			if err := encodeOne(ctx, inputPath, path, stream, videoDurationSecs); err != nil {
				cancel()
				errCh <- fmt.Errorf("stream %d: %w", stream.StreamIndex, err)
				return
			}
			results[i] = EncodedStream{Info: stream, Path: path}
		}()
	}

	wg.Wait()
	close(errCh)
	if err := <-errCh; err != nil {
		return nil, err
	}
	return results, nil
}

// AudioPath returns the per-stream Opus output path.
func AudioPath(workDir string, outputIndex int) string {
	return filepath.Join(workDir, fmt.Sprintf("audio_%02d.opus", outputIndex))
}

func encodeOne(ctx context.Context, inputPath, outputPath string, stream media.AudioStreamInfo, videoDurationSecs float64) error {
	if stream.Channels == 0 {
		return fmt.Errorf("audio stream has no channels")
	}
	if stream.Channels > 8 {
		return fmt.Errorf("unsupported audio channel count: %d channels", stream.Channels)
	}

	dec, err := openDecoder(inputPath, stream.StreamIndex)
	if err != nil {
		return err
	}
	defer dec.close()

	enc, err := newOpusEncoder(outputPath, stream.Channels, CalculateBitrate(stream.Channels))
	if err != nil {
		return err
	}

	channels := int(stream.Channels)
	if err := dec.decodeTo(ctx, audioSampleLimit(videoDurationSecs, stream.StartOffsetSecs), func(pcm []float32) error {
		if channels > 2 {
			reorderSurround(pcm, channels)
		}
		return enc.writeFloat(pcm, channels)
	}); err != nil {
		enc.destroy()
		return err
	}
	return enc.close()
}

// audioSampleLimit bounds PCM to the video timeline; a later-starting track
// has correspondingly less room before the final video frame.
func audioSampleLimit(videoDurationSecs, startOffsetSecs float64) int64 {
	return int64(math.Max(0, math.Round((videoDurationSecs-startOffsetSecs)*outputSampleRate)))
}

func reorderSurround(buf []float32, channels int) {
	var order []int
	switch channels {
	case 6:
		order = []int{0, 2, 1, 4, 5, 3}
	case 7:
		order = []int{0, 2, 1, 5, 6, 4, 3}
	case 8:
		order = []int{0, 2, 1, 6, 7, 4, 5, 3}
	default:
		return
	}

	var tmp [8]float32
	for sample := 0; sample < len(buf)/channels; sample++ {
		base := sample * channels
		for out, in := range order {
			tmp[out] = buf[base+in]
		}
		copy(buf[base:base+channels], tmp[:channels])
	}
}

func avError(code C.int) string {
	buf := make([]C.char, 256)
	C.reel_audio_strerror(code, &buf[0], C.size_t(len(buf)))
	return strings.TrimRight(C.GoString(&buf[0]), "\x00")
}
