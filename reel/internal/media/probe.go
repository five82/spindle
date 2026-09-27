// Package media provides native libav media probing helpers.
package media

/*
#cgo pkg-config: libavformat libavcodec libavutil
#include <errno.h>
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/dict.h>
#include <libavutil/error.h>
#include <libavutil/log.h>
#include <libavutil/mathematics.h>
#include <libavutil/opt.h>
#include <libavutil/pixdesc.h>
#include <stdlib.h>

static void reel_probe_init_av_log(void) {
	av_log_set_level(AV_LOG_ERROR);
}

static void reel_probe_set_probe_size(AVFormatContext *ctx) {
	if (ctx != NULL) {
		av_opt_set_int(ctx, "probesize", 0x80000, AV_OPT_SEARCH_CHILDREN);
	}
}

static AVStream* reel_probe_stream_at(AVFormatContext *ctx, int idx) {
	return ctx->streams[idx];
}

static const char* reel_probe_metadata(AVStream *stream, const char *key) {
	AVDictionaryEntry *entry = av_dict_get(stream->metadata, key, NULL, AV_DICT_IGNORE_SUFFIX);
	if (entry == NULL) {
		return NULL;
	}
	return entry->value;
}

static const char* reel_probe_codec_name(const AVCodecParameters *par) {
	return avcodec_get_name(par->codec_id);
}

static const char* reel_probe_profile_name(const AVCodecParameters *par) {
	return avcodec_profile_name(par->codec_id, par->profile);
}

static int reel_probe_channels(const AVCodecParameters *par) {
	return par->ch_layout.nb_channels;
}

static int reel_probe_bit_depth(const AVCodecParameters *par) {
	if (par->bits_per_raw_sample > 0) {
		return par->bits_per_raw_sample;
	}
	if (par->format >= 0) {
		const AVPixFmtDescriptor *desc = av_pix_fmt_desc_get((enum AVPixelFormat)par->format);
		if (desc != NULL) {
			return desc->comp[0].depth;
		}
	}
	return 0;
}

static const char* reel_probe_color_primaries_name(int value) {
	return av_color_primaries_name((enum AVColorPrimaries)value);
}

static const char* reel_probe_color_transfer_name(int value) {
	return av_color_transfer_name((enum AVColorTransferCharacteristic)value);
}

static const char* reel_probe_color_space_name(int value) {
	return av_color_space_name((enum AVColorSpace)value);
}

static void reel_probe_strerror(int errnum, char *buf, size_t buflen) {
	av_strerror(errnum, buf, buflen);
}

static int reel_probe_averror_eof(void) {
	return AVERROR_EOF;
}

static int reel_probe_stream_start_us(AVStream *stream, int64_t *start_us) {
	if (stream == NULL || stream->start_time == AV_NOPTS_VALUE) {
		return 0;
	}
	*start_us = av_rescale_q(stream->start_time, stream->time_base, AV_TIME_BASE_Q);
	return 1;
}
*/
import "C"

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

const avTimeBase = 1000000

var initOnce sync.Once

func initLibav() {
	initOnce.Do(func() {
		C.reel_probe_init_av_log()
	})
}

// VideoProperties contains video stream properties.
type VideoProperties struct {
	Width                uint32
	Height               uint32
	DurationSecs         float64
	SampleAspectRatioNum uint32
	SampleAspectRatioDen uint32
	HDRInfo              HDRInfo
}

// HDRInfo contains HDR-related information.
type HDRInfo struct {
	IsHDR                   bool
	ColourPrimaries         string
	TransferCharacteristics string
	MatrixCoefficients      string
	BitDepth                *uint8
}

// AudioStreamInfo contains information about an audio stream.
type AudioStreamInfo struct {
	Channels        uint32
	CodecName       string
	Profile         string
	Index           int // zero-based audio stream ordinal
	StreamIndex     int // actual container stream index
	Language        string
	Title           string
	IsSpatial       bool    // Always false (spatial support removed)
	StartOffsetSecs float64 // Relative to the best video stream
	DurationSecs    float64
	Disposition     StreamDisposition
}

// StreamDisposition contains stream disposition flags.
type StreamDisposition struct {
	Default         int `json:"default"`
	Dub             int `json:"dub"`
	Original        int `json:"original"`
	Comment         int `json:"comment"`
	Lyrics          int `json:"lyrics"`
	Karaoke         int `json:"karaoke"`
	Forced          int `json:"forced"`
	HearingImpaired int `json:"hearing_impaired"`
	VisualImpaired  int `json:"visual_impaired"`
	CleanEffects    int `json:"clean_effects"`
	AttachedPic     int `json:"attached_pic"`
	TimedThumbnails int `json:"timed_thumbnails"`
}

// GetVideoProperties returns video properties including HDR info.
func GetVideoProperties(inputPath string) (*VideoProperties, error) {
	fmtCtx, err := openInput(inputPath)
	if err != nil {
		return nil, err
	}
	defer C.avformat_close_input(&fmtCtx)

	stream, err := bestStream(fmtCtx, C.AVMEDIA_TYPE_VIDEO)
	if err != nil {
		return nil, err
	}
	par := stream.codecpar

	if par.width <= 0 || par.height <= 0 {
		return nil, fmt.Errorf("invalid dimensions in %s: %dx%d", inputPath, int(par.width), int(par.height))
	}

	bitDepth := bitDepth(par)
	primaries := cString(C.reel_probe_color_primaries_name(C.int(par.color_primaries)))
	transfer := cString(C.reel_probe_color_transfer_name(C.int(par.color_trc)))
	matrix := cString(C.reel_probe_color_space_name(C.int(par.color_space)))

	sarNum, sarDen := sampleAspectRatio(stream, par)
	hdrInfo := HDRInfo{
		ColourPrimaries:         primaries,
		TransferCharacteristics: transfer,
		MatrixCoefficients:      matrix,
		BitDepth:                bitDepth,
		IsHDR:                   detectHDR(primaries, transfer, matrix),
	}

	return &VideoProperties{
		Width:                uint32(par.width),
		Height:               uint32(par.height),
		DurationSecs:         durationSecs(fmtCtx, stream),
		SampleAspectRatioNum: sarNum,
		SampleAspectRatioDen: sarDen,
		HDRInfo:              hdrInfo,
	}, nil
}

// GetAudioStreamInfo returns detailed audio stream information.
func GetAudioStreamInfo(inputPath string) ([]AudioStreamInfo, error) {
	fmtCtx, err := openInput(inputPath)
	if err != nil {
		return nil, err
	}
	defer C.avformat_close_input(&fmtCtx)

	videoStartUS := int64(0)
	if videoStream, err := bestStream(fmtCtx, C.AVMEDIA_TYPE_VIDEO); err == nil {
		if startUS, ok := streamStartUS(videoStream); ok {
			videoStartUS = startUS
		}
	}

	streams := make([]AudioStreamInfo, 0)
	for i := 0; i < int(fmtCtx.nb_streams); i++ {
		stream := C.reel_probe_stream_at(fmtCtx, C.int(i))
		par := stream.codecpar
		if par.codec_type != C.AVMEDIA_TYPE_AUDIO {
			continue
		}

		channels := int(C.reel_probe_channels(par))
		if channels <= 0 {
			continue
		}

		startOffsetSecs := 0.0
		if startUS, ok := streamStartUS(stream); ok {
			startOffsetSecs = float64(startUS-videoStartUS) / avTimeBase
		}

		streams = append(streams, AudioStreamInfo{
			Channels:        uint32(channels),
			CodecName:       cString(C.reel_probe_codec_name(par)),
			Profile:         cString(C.reel_probe_profile_name(par)),
			Index:           len(streams),
			StreamIndex:     int(stream.index),
			Language:        metadata(stream, "language"),
			Title:           metadata(stream, "title"),
			IsSpatial:       false,
			StartOffsetSecs: startOffsetSecs,
			DurationSecs:    durationSecs(fmtCtx, stream),
			Disposition:     streamDisposition(int(stream.disposition)),
		})
	}

	return streams, nil
}

// GetVideoCodecName returns the video codec name for a file.
func GetVideoCodecName(inputPath string) (string, error) {
	fmtCtx, err := openInput(inputPath)
	if err != nil {
		return "", err
	}
	defer C.avformat_close_input(&fmtCtx)

	stream, err := bestStream(fmtCtx, C.AVMEDIA_TYPE_VIDEO)
	if err != nil {
		return "", err
	}
	return cString(C.reel_probe_codec_name(stream.codecpar)), nil
}

// GetVideoStreamBytes returns the total payload bytes in the best video stream.
func GetVideoStreamBytes(inputPath string) (uint64, error) {
	fmtCtx, err := openInput(inputPath)
	if err != nil {
		return 0, err
	}
	defer C.avformat_close_input(&fmtCtx)

	stream, err := bestStream(fmtCtx, C.AVMEDIA_TYPE_VIDEO)
	if err != nil {
		return 0, err
	}
	streamIndex := int(stream.index)

	pkt := C.av_packet_alloc()
	if pkt == nil {
		return 0, fmt.Errorf("probe: packet allocation failed")
	}
	defer C.av_packet_free(&pkt)

	var total uint64
	for {
		ret := C.av_read_frame(fmtCtx, pkt)
		if ret < 0 {
			if ret == C.reel_probe_averror_eof() {
				return total, nil
			}
			return total, fmt.Errorf("probe: read packet failed: %s", avError(ret))
		}
		if int(pkt.stream_index) == streamIndex && pkt.size > 0 {
			total += uint64(pkt.size)
		}
		C.av_packet_unref(pkt)
	}
}

func openInput(path string) (*C.AVFormatContext, error) {
	initLibav()

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var fmtCtx *C.AVFormatContext
	if ret := C.avformat_open_input(&fmtCtx, cPath, nil, nil); ret < 0 {
		return nil, fmt.Errorf("probe: open failed: %s", avError(ret))
	}
	C.reel_probe_set_probe_size(fmtCtx)
	if ret := C.avformat_find_stream_info(fmtCtx, nil); ret < 0 {
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("probe: stream info failed: %s", avError(ret))
	}
	return fmtCtx, nil
}

func bestStream(fmtCtx *C.AVFormatContext, mediaType C.enum_AVMediaType) (*C.AVStream, error) {
	idx := C.av_find_best_stream(fmtCtx, mediaType, -1, -1, nil, 0)
	if idx < 0 {
		return nil, fmt.Errorf("no %s stream found", mediaTypeName(mediaType))
	}
	return C.reel_probe_stream_at(fmtCtx, idx), nil
}

func mediaTypeName(mediaType C.enum_AVMediaType) string {
	if mediaType == C.AVMEDIA_TYPE_VIDEO {
		return "video"
	}
	if mediaType == C.AVMEDIA_TYPE_AUDIO {
		return "audio"
	}
	return "requested"
}

func streamStartUS(stream *C.AVStream) (int64, bool) {
	var startUS C.int64_t
	if C.reel_probe_stream_start_us(stream, &startUS) == 0 {
		return 0, false
	}
	return int64(startUS), true
}

func durationSecs(fmtCtx *C.AVFormatContext, stream *C.AVStream) float64 {
	if stream != nil {
		if duration := parseDurationTag(metadata(stream, "DURATION")); duration > 0 {
			return duration
		}
		if stream.duration > 0 && stream.time_base.num > 0 && stream.time_base.den > 0 {
			return float64(stream.duration) * float64(stream.time_base.num) / float64(stream.time_base.den)
		}
	}
	if fmtCtx.duration > 0 {
		return float64(fmtCtx.duration) / avTimeBase
	}
	return 0
}

func parseDurationTag(value string) float64 {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 3 {
		return 0
	}
	hours, err := strconv.ParseFloat(parts[0], 64)
	if err != nil || hours < 0 {
		return 0
	}
	minutes, err := strconv.ParseFloat(parts[1], 64)
	if err != nil || minutes < 0 || minutes >= 60 {
		return 0
	}
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil || seconds < 0 || seconds >= 60 {
		return 0
	}
	return hours*3600 + minutes*60 + seconds
}

func sampleAspectRatio(stream *C.AVStream, par *C.AVCodecParameters) (uint32, uint32) {
	if stream.sample_aspect_ratio.num > 0 && stream.sample_aspect_ratio.den > 0 {
		return uint32(stream.sample_aspect_ratio.num), uint32(stream.sample_aspect_ratio.den)
	}
	if par.sample_aspect_ratio.num > 0 && par.sample_aspect_ratio.den > 0 {
		return uint32(par.sample_aspect_ratio.num), uint32(par.sample_aspect_ratio.den)
	}
	return 0, 0
}

func bitDepth(par *C.AVCodecParameters) *uint8 {
	depth := int(C.reel_probe_bit_depth(par))
	if depth <= 0 || depth > 255 {
		return nil
	}
	value := uint8(depth)
	return &value
}

func metadata(stream *C.AVStream, key string) string {
	cKey := C.CString(key)
	defer C.free(unsafe.Pointer(cKey))
	return cString(C.reel_probe_metadata(stream, cKey))
}

func cString(s *C.char) string {
	if s == nil {
		return ""
	}
	return C.GoString(s)
}

func streamDisposition(disposition int) StreamDisposition {
	enabled := func(flag C.int) int {
		if disposition&int(flag) != 0 {
			return 1
		}
		return 0
	}
	return StreamDisposition{
		Default:         enabled(C.AV_DISPOSITION_DEFAULT),
		Dub:             enabled(C.AV_DISPOSITION_DUB),
		Original:        enabled(C.AV_DISPOSITION_ORIGINAL),
		Comment:         enabled(C.AV_DISPOSITION_COMMENT),
		Lyrics:          enabled(C.AV_DISPOSITION_LYRICS),
		Karaoke:         enabled(C.AV_DISPOSITION_KARAOKE),
		Forced:          enabled(C.AV_DISPOSITION_FORCED),
		HearingImpaired: enabled(C.AV_DISPOSITION_HEARING_IMPAIRED),
		VisualImpaired:  enabled(C.AV_DISPOSITION_VISUAL_IMPAIRED),
		CleanEffects:    enabled(C.AV_DISPOSITION_CLEAN_EFFECTS),
		AttachedPic:     enabled(C.AV_DISPOSITION_ATTACHED_PIC),
		TimedThumbnails: enabled(C.AV_DISPOSITION_TIMED_THUMBNAILS),
	}
}

func avError(ret C.int) string {
	buf := (*C.char)(C.malloc(256))
	defer C.free(unsafe.Pointer(buf))
	C.reel_probe_strerror(ret, buf, 256)
	return C.GoString(buf)
}

// detectHDR determines if content is HDR based on color metadata.
func detectHDR(primaries, transfer, matrix string) bool {
	// Check for HDR primaries (BT.2020)
	if containsCI(primaries, "bt2020") || containsCI(primaries, "bt.2020") || containsCI(primaries, "bt2100") {
		return true
	}

	// Check for HDR transfer characteristics (PQ, HLG)
	if containsCI(transfer, "pq") || containsCI(transfer, "smpte2084") || containsCI(transfer, "smpte 2084") || containsCI(transfer, "hlg") || containsCI(transfer, "arib-std-b67") {
		return true
	}

	// Check for HDR matrix coefficients
	if containsCI(matrix, "bt2020") || containsCI(matrix, "bt.2020") {
		return true
	}

	return false
}

// containsCI performs a case-insensitive substring check.
func containsCI(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
