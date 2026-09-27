package audio

/*
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libswresample/swresample.h>
#include <stdlib.h>

AVStream* reel_audio_stream_at(AVFormatContext *ctx, int idx);
int reel_audio_find_stream_pos(AVFormatContext *ctx, int stream_index);
void reel_audio_discard_except(AVFormatContext *ctx, int stream_pos);
int reel_audio_channels(const AVCodecParameters *par);
int reel_swr_alloc_for_stream(struct SwrContext **swr, const AVChannelLayout *layout, int in_fmt, int in_rate);
int reel_swr_convert_frame(struct SwrContext *swr, float *out, int out_count, const AVFrame *frame);
int reel_swr_flush(struct SwrContext *swr, float *out, int out_count);
int reel_audio_averror_eagain(void);
int reel_audio_averror_eof(void);
void reel_audio_strerror(int errnum, char *buf, size_t buflen);
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"unsafe"
)

type decoder struct {
	fmtCtx    *C.AVFormatContext
	codecCtx  *C.AVCodecContext
	swr       *C.struct_SwrContext
	pkt       *C.AVPacket
	frame     *C.AVFrame
	streamPos C.int
	channels  int
}

func openDecoder(inputPath string, streamIndex int) (*decoder, error) {
	initLibav()

	cPath := C.CString(inputPath)
	defer C.free(unsafe.Pointer(cPath))

	var fmtCtx *C.AVFormatContext
	if ret := C.avformat_open_input(&fmtCtx, cPath, nil, nil); ret < 0 {
		return nil, fmt.Errorf("audio decoder: open failed: %s", avError(ret))
	}

	if ret := C.avformat_find_stream_info(fmtCtx, nil); ret < 0 {
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("audio decoder: stream info failed: %s", avError(ret))
	}

	streamPos := C.reel_audio_find_stream_pos(fmtCtx, C.int(streamIndex))
	if streamPos < 0 {
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("audio decoder: stream %d not found", streamIndex)
	}
	C.reel_audio_discard_except(fmtCtx, streamPos)

	stream := C.reel_audio_stream_at(fmtCtx, streamPos)
	par := stream.codecpar
	codec := C.avcodec_find_decoder(par.codec_id)
	if codec == nil {
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("audio decoder: unsupported codec")
	}

	codecCtx := C.avcodec_alloc_context3(codec)
	if codecCtx == nil {
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("audio decoder: alloc codec failed")
	}
	if ret := C.avcodec_parameters_to_context(codecCtx, par); ret < 0 {
		C.avcodec_free_context(&codecCtx)
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("audio decoder: copy codec parameters failed: %s", avError(ret))
	}
	if ret := C.avcodec_open2(codecCtx, codec, nil); ret < 0 {
		C.avcodec_free_context(&codecCtx)
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("audio decoder: codec open failed: %s", avError(ret))
	}

	var swr *C.struct_SwrContext
	if ret := C.reel_swr_alloc_for_stream(&swr, &par.ch_layout, C.int(par.format), C.int(par.sample_rate)); ret < 0 {
		C.avcodec_free_context(&codecCtx)
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("audio decoder: swr alloc failed: %s", avError(ret))
	}
	if ret := C.swr_init(swr); ret < 0 {
		C.swr_free(&swr)
		C.avcodec_free_context(&codecCtx)
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("audio decoder: swr init failed: %s", avError(ret))
	}

	pkt := C.av_packet_alloc()
	frame := C.av_frame_alloc()
	if pkt == nil || frame == nil {
		if pkt != nil {
			C.av_packet_free(&pkt)
		}
		if frame != nil {
			C.av_frame_free(&frame)
		}
		C.swr_free(&swr)
		C.avcodec_free_context(&codecCtx)
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("audio decoder: frame allocation failed")
	}

	return &decoder{
		fmtCtx:    fmtCtx,
		codecCtx:  codecCtx,
		swr:       swr,
		pkt:       pkt,
		frame:     frame,
		streamPos: streamPos,
		channels:  int(C.reel_audio_channels(par)),
	}, nil
}

var errDecodeLimitReached = errors.New("audio decode limit reached")

func (d *decoder) decodeTo(ctx context.Context, maxSamples int64, cb func([]float32) error) error {
	if maxSamples <= 0 {
		return nil
	}
	const maxOutSamples = 96000
	out := make([]float32, maxOutSamples*d.channels)
	written := int64(0)
	emit := func(pcm []float32) error {
		remaining := maxSamples - written
		if remaining <= 0 {
			return errDecodeLimitReached
		}
		samples := min(int64(len(pcm)/d.channels), remaining)
		if err := cb(pcm[:int(samples)*d.channels]); err != nil {
			return err
		}
		written += samples
		if written == maxSamples {
			return errDecodeLimitReached
		}
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		ret := C.av_read_frame(d.fmtCtx, d.pkt)
		if ret < 0 {
			break
		}

		if d.pkt.stream_index != d.streamPos {
			C.av_packet_unref(d.pkt)
			continue
		}

		sendRet := C.avcodec_send_packet(d.codecCtx, d.pkt)
		for sendRet == C.reel_audio_averror_eagain() {
			if err := d.drainFrames(out, emit); err != nil {
				C.av_packet_unref(d.pkt)
				if errors.Is(err, errDecodeLimitReached) {
					return nil
				}
				return err
			}
			sendRet = C.avcodec_send_packet(d.codecCtx, d.pkt)
		}
		if sendRet < 0 {
			C.av_packet_unref(d.pkt)
			return fmt.Errorf("audio decoder: send packet failed: %s", avError(sendRet))
		}
		C.av_packet_unref(d.pkt)

		if err := d.drainFrames(out, emit); err != nil {
			if errors.Is(err, errDecodeLimitReached) {
				return nil
			}
			return err
		}
	}

	if ret := C.avcodec_send_packet(d.codecCtx, nil); ret < 0 && ret != C.reel_audio_averror_eof() {
		return fmt.Errorf("audio decoder: flush failed: %s", avError(ret))
	}
	if err := d.drainFrames(out, emit); err != nil {
		if errors.Is(err, errDecodeLimitReached) {
			return nil
		}
		return err
	}

	for {
		n := C.reel_swr_flush(d.swr, (*C.float)(unsafe.Pointer(&out[0])), C.int(maxOutSamples))
		if n < 0 {
			return fmt.Errorf("audio decoder: swr flush failed: %s", avError(n))
		}
		if n == 0 {
			return nil
		}
		if err := emit(out[:int(n)*d.channels]); err != nil {
			if errors.Is(err, errDecodeLimitReached) {
				return nil
			}
			return err
		}
	}
}

func (d *decoder) drainFrames(out []float32, cb func([]float32) error) error {
	for {
		ret := C.avcodec_receive_frame(d.codecCtx, d.frame)
		if ret == C.reel_audio_averror_eagain() || ret == C.reel_audio_averror_eof() {
			return nil
		}
		if ret < 0 {
			return fmt.Errorf("audio decoder: decode failed: %s", avError(ret))
		}

		n := C.reel_swr_convert_frame(d.swr, (*C.float)(unsafe.Pointer(&out[0])), C.int(len(out)/d.channels), d.frame)
		C.av_frame_unref(d.frame)
		if n < 0 {
			return fmt.Errorf("audio decoder: swr convert failed: %s", avError(n))
		}
		if n > 0 {
			if err := cb(out[:int(n)*d.channels]); err != nil {
				return err
			}
		}
	}
}

func (d *decoder) close() {
	if d == nil {
		return
	}
	if d.swr != nil {
		C.swr_free(&d.swr)
	}
	if d.frame != nil {
		C.av_frame_free(&d.frame)
	}
	if d.pkt != nil {
		C.av_packet_free(&d.pkt)
	}
	if d.codecCtx != nil {
		C.avcodec_free_context(&d.codecCtx)
	}
	if d.fmtCtx != nil {
		C.avformat_close_input(&d.fmtCtx)
	}
}
