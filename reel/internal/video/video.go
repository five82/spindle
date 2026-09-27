// Package video provides FFmpeg/libav based video probing and frame extraction.
package video

/*
#cgo pkg-config: libavformat libavcodec libavutil libavfilter libswscale
#include <errno.h>
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libavutil/imgutils.h>
#include <libavutil/log.h>
#include <libavutil/mastering_display_metadata.h>
#include <libavutil/opt.h>
#include <libavutil/pixdesc.h>
#include <libavfilter/avfilter.h>
#include <libavfilter/buffersink.h>
#include <libavfilter/buffersrc.h>
#include <libswscale/swscale.h>
#include <stdlib.h>

static AVStream* reel_stream_at(AVFormatContext *ctx, int idx) {
	return ctx->streams[idx];
}

static void reel_discard_non_video_streams(AVFormatContext *ctx) {
	for (unsigned int i = 0; i < ctx->nb_streams; i++) {
		AVStream *stream = ctx->streams[i];
		if (stream->codecpar->codec_type != AVMEDIA_TYPE_VIDEO) {
			stream->discard = AVDISCARD_ALL;
		}
	}
}

static void reel_init_av_log(void) {
	av_log_set_level(AV_LOG_ERROR);
}

static void reel_set_probe_size(AVFormatContext *ctx) {
	if (ctx != NULL) {
		av_opt_set_int(ctx, "probesize", 0x80000, AV_OPT_SEARCH_CHILDREN);
	}
}

static int reel_averror_eagain(void) {
	return AVERROR(EAGAIN);
}

static int reel_averror_eof(void) {
	return AVERROR_EOF;
}

static void reel_strerror(int errnum, char *buf, size_t buflen) {
	av_strerror(errnum, buf, buflen);
}

static int reel_pix_fmt_depth(int fmt) {
	const AVPixFmtDescriptor *desc = av_pix_fmt_desc_get((enum AVPixelFormat)fmt);
	if (desc == NULL) {
		return 0;
	}
	return desc->comp[0].depth;
}

static int reel_sws_scale(struct SwsContext *ctx, const AVFrame *src, AVFrame *dst) {
	return sws_scale(ctx, (const uint8_t * const*)src->data, src->linesize, 0, src->height, dst->data, dst->linesize);
}

static const char* reel_pix_fmt_name(int fmt) {
	return av_get_pix_fmt_name((enum AVPixelFormat)fmt);
}

// reel_filter_init builds "buffer -> desc -> buffersink" for one frame geometry.
// The graph is pinned to a single thread so a temporal filter sees a
// deterministic frame history.
static int reel_filter_init(AVFilterGraph **graph_out, AVFilterContext **src_out, AVFilterContext **sink_out,
                            const char *desc, int width, int height, int pix_fmt,
                            int tb_num, int tb_den, int sar_num, int sar_den) {
	char args[256];
	AVFilterGraph *graph = NULL;
	AVFilterContext *src_ctx = NULL;
	AVFilterContext *sink_ctx = NULL;
	AVFilterInOut *outputs = NULL;
	AVFilterInOut *inputs = NULL;
	const AVFilter *buffersrc = avfilter_get_by_name("buffer");
	const AVFilter *buffersink = avfilter_get_by_name("buffersink");
	int ret;

	*graph_out = NULL;
	*src_out = NULL;
	*sink_out = NULL;
	if (buffersrc == NULL || buffersink == NULL) {
		return AVERROR_FILTER_NOT_FOUND;
	}
	graph = avfilter_graph_alloc();
	outputs = avfilter_inout_alloc();
	inputs = avfilter_inout_alloc();
	if (graph == NULL || outputs == NULL || inputs == NULL) {
		ret = AVERROR(ENOMEM);
		goto fail;
	}
	graph->nb_threads = 1;

	snprintf(args, sizeof(args), "video_size=%dx%d:pix_fmt=%d:time_base=%d/%d:pixel_aspect=%d/%d",
	         width, height, pix_fmt, tb_num, tb_den, sar_num, sar_den);
	ret = avfilter_graph_create_filter(&src_ctx, buffersrc, "in", args, NULL, graph);
	if (ret < 0) {
		goto fail;
	}
	ret = avfilter_graph_create_filter(&sink_ctx, buffersink, "out", NULL, NULL, graph);
	if (ret < 0) {
		goto fail;
	}

	outputs->name = av_strdup("in");
	outputs->filter_ctx = src_ctx;
	outputs->pad_idx = 0;
	outputs->next = NULL;
	inputs->name = av_strdup("out");
	inputs->filter_ctx = sink_ctx;
	inputs->pad_idx = 0;
	inputs->next = NULL;
	if (outputs->name == NULL || inputs->name == NULL) {
		ret = AVERROR(ENOMEM);
		goto fail;
	}

	ret = avfilter_graph_parse_ptr(graph, desc, &inputs, &outputs, NULL);
	if (ret < 0) {
		goto fail;
	}
	ret = avfilter_graph_config(graph, NULL);
	if (ret < 0) {
		goto fail;
	}

	avfilter_inout_free(&inputs);
	avfilter_inout_free(&outputs);
	*graph_out = graph;
	*src_out = src_ctx;
	*sink_out = sink_ctx;
	return 0;

fail:
	avfilter_inout_free(&inputs);
	avfilter_inout_free(&outputs);
	avfilter_graph_free(&graph);
	return ret;
}

static int reel_filter_frame(AVFilterContext *src_ctx, AVFilterContext *sink_ctx, AVFrame *in, AVFrame *out, int64_t pts) {
	int64_t saved_pts = in->pts;
	int ret;

	// Frames are pushed with a synthetic frame-index pts so repeated or
	// timestamp-less decoder frames stay strictly monotonic for buffersrc.
	in->pts = pts;
	ret = av_buffersrc_add_frame_flags(src_ctx, in, AV_BUFFERSRC_FLAG_KEEP_REF);
	in->pts = saved_pts;
	if (ret < 0) {
		return ret;
	}
	return av_buffersink_get_frame(sink_ctx, out);
}
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"unsafe"
)

const (
	avTimeBase               = 1000000
	avColorBT709             = 1
	avColorUnspecified       = 2
	avColorReserved          = 3
	svtColorRangeLimited     = 0
	svtColorRangeFull        = 1
	svtChromaPositionLeft    = 1
	svtChromaPositionTopLeft = 2

	seekPrerollFrames = 30

	// noFilteredFrame marks "no sequential predecessor" for the denoise filter;
	// any valid frame index makes the sequential check fail against it.
	noFilteredFrame = -2
)

var initOnce sync.Once

func initLibav() {
	initOnce.Do(func() {
		C.reel_init_av_log()
	})
}

// Info contains video properties and HDR metadata.
type Info struct {
	Width                   uint32
	Height                  uint32
	FPSNum                  uint32
	FPSDen                  uint32
	Frames                  int
	SampleAspectRatioNum    uint32
	SampleAspectRatioDen    uint32
	ColorPrimaries          *int32
	TransferCharacteristics *int32
	MatrixCoefficients      *int32
	ColorRange              *int32
	ChromaSamplePosition    *int32
	Is10Bit                 bool
	MasteringDisplay        *string
	ContentLight            *string
	PixelFormat             int
}

// Source is an owned video decoder. It is not safe for concurrent use.
type Source struct {
	fmtCtx       *C.AVFormatContext
	codecCtx     *C.AVCodecContext
	pkt          *C.AVPacket
	frame        *C.AVFrame
	lastFrame    *C.AVFrame
	pendingFrame *C.AVFrame
	convFrame    *C.AVFrame
	swsCtx       *C.struct_SwsContext
	swsFormat    int

	// Experimental pre-encode denoise. filterDesc is a libavfilter graph string
	// applied to every ReadFrame result, so the encoder input and the quality
	// metric reference are the same filtered pixels.
	filterDesc      string
	filterGraph     *C.AVFilterGraph
	filterSrcCtx    *C.AVFilterContext
	filterSinkCtx   *C.AVFilterContext
	filterFrame     *C.AVFrame
	filterFormat    int
	filterWidth     C.int
	filterHeight    C.int
	lastFilteredIdx int

	streamIdx  int
	nextFrame  int
	eof        bool
	hasLast    bool
	hasPending bool
	pendingIdx int
	startTime  int64
	tsMul      int64
	tsDiv      int64
	fpsNum     int
	fpsDen     int
}

// CropRect describes the exact source rectangle to encode.
type CropRect struct {
	X      uint32 // Left offset in source pixels
	Y      uint32 // Top offset in source pixels
	Width  uint32 // Output width in pixels
	Height uint32 // Output height in pixels
}

// CropCalc contains crop calculation parameters for frame extraction.
type CropCalc struct {
	NewW  uint32 // Cropped width
	NewH  uint32 // Cropped height
	CropX uint32 // Left crop offset
	CropY uint32 // Top crop offset
}

// LumaFrame is a borrowed view of a decoded frame's Y plane.
// Data remains valid until the next frame request on the same Source or until the Source is closed.
type LumaFrame struct {
	Data      []byte
	Stride    int
	Width     int
	Height    int
	Is10Bit   bool
	LumaShift int // Right shift needed to convert native samples to 8-bit values.
}

// Probe reads video stream properties and first-frame metadata.
func Probe(path string) (*Info, error) {
	src, err := Open(path, 0)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	stream := C.reel_stream_at(src.fmtCtx, C.int(src.streamIdx))
	par := stream.codecpar
	fps := stream.avg_frame_rate
	if fps.num <= 0 || fps.den <= 0 {
		fps = stream.r_frame_rate
	}
	if fps.num <= 0 || fps.den <= 0 {
		fps = par.framerate
	}

	inf := &Info{
		Width:       uint32(par.width),
		Height:      uint32(par.height),
		FPSNum:      uint32(fps.num),
		FPSDen:      uint32(fps.den),
		Frames:      src.frameCount(stream, fps),
		PixelFormat: int(par.format),
	}
	if stream.sample_aspect_ratio.num > 0 && stream.sample_aspect_ratio.den > 0 {
		inf.SampleAspectRatioNum = uint32(stream.sample_aspect_ratio.num)
		inf.SampleAspectRatioDen = uint32(stream.sample_aspect_ratio.den)
	} else if par.sample_aspect_ratio.num > 0 && par.sample_aspect_ratio.den > 0 {
		inf.SampleAspectRatioNum = uint32(par.sample_aspect_ratio.num)
		inf.SampleAspectRatioDen = uint32(par.sample_aspect_ratio.den)
	}

	frame, _, err := src.decodeOne()
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to decode first frame: %w", err)
	}
	if frame != nil {
		inf.PixelFormat = int(frame.format)
		inf.Is10Bit = pixelDepth(int(frame.format)) >= 10
		src.fillFrameMetadata(inf, frame, par)
	} else {
		inf.Is10Bit = pixelDepth(int(par.format)) >= 10
	}

	return inf, nil
}

// Open opens a video decoder for path.
func Open(path string, threads int) (*Source, error) {
	return OpenFiltered(path, threads, "")
}

// OpenFiltered opens a video decoder that pushes every ReadFrame result through
// filterDesc, a libavfilter graph string such as "hqdn3d=2:1.5:3:2.25". An empty
// filterDesc disables filtering.
//
// Only ReadFrame is filtered: encoder input and quality-metric reference frames
// both come from ReadFrame, so scoring compares the encode against the same
// denoised pixels the encoder saw. ReadLumaFrame/ReadLumaFrameNear stay
// unfiltered on purpose so shot-cut detection and crop detection keep measuring
// the real source.
//
// Temporal filters (hqdn3d, atadenoise) carry state across frames, so the graph
// is rebuilt whenever a read is non-sequential, and callers must call
// ResetFilter at every chunk boundary; see ResetFilter.
func OpenFiltered(path string, threads int, filterDesc string) (*Source, error) {
	initLibav()

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var fmtCtx *C.AVFormatContext
	if ret := C.avformat_open_input(&fmtCtx, cPath, nil, nil); ret < 0 {
		return nil, fmt.Errorf("decoder: open failed: %s", avError(ret))
	}

	C.reel_set_probe_size(fmtCtx)
	C.reel_discard_non_video_streams(fmtCtx)
	if ret := C.avformat_find_stream_info(fmtCtx, nil); ret < 0 {
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("decoder: stream info failed: %s", avError(ret))
	}

	streamIdx := C.av_find_best_stream(fmtCtx, C.AVMEDIA_TYPE_VIDEO, -1, -1, nil, 0)
	if streamIdx < 0 {
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("decoder: no video stream: %s", avError(streamIdx))
	}

	stream := C.reel_stream_at(fmtCtx, streamIdx)
	par := stream.codecpar
	codec := C.avcodec_find_decoder(par.codec_id)
	if codec == nil {
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("decoder: unsupported codec")
	}

	codecCtx := C.avcodec_alloc_context3(codec)
	if codecCtx == nil {
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("decoder: alloc codec failed")
	}
	if ret := C.avcodec_parameters_to_context(codecCtx, par); ret < 0 {
		C.avcodec_free_context(&codecCtx)
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("decoder: copy codec parameters failed: %s", avError(ret))
	}
	if threads > 0 {
		codecCtx.thread_count = C.int(threads)
	}

	// Decode without applying film grain synthesis (AV1 FGS): quality
	// metrics must score the grain-free reconstruction, since synthetic
	// grain is random noise relative to any reference and would read as
	// error. The encoder's delivered pixels are that same reconstruction;
	// grain is applied by the playback decoder. No-op for codecs without
	// film grain metadata.
	codecCtx.export_side_data |= C.AV_CODEC_EXPORT_DATA_FILM_GRAIN

	if ret := C.avcodec_open2(codecCtx, codec, nil); ret < 0 {
		C.avcodec_free_context(&codecCtx)
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("decoder: codec open failed: %s", avError(ret))
	}

	pkt := C.av_packet_alloc()
	frame := C.av_frame_alloc()
	lastFrame := C.av_frame_alloc()
	pendingFrame := C.av_frame_alloc()
	if pkt == nil || frame == nil || lastFrame == nil || pendingFrame == nil {
		if pkt != nil {
			C.av_packet_free(&pkt)
		}
		if frame != nil {
			C.av_frame_free(&frame)
		}
		if lastFrame != nil {
			C.av_frame_free(&lastFrame)
		}
		if pendingFrame != nil {
			C.av_frame_free(&pendingFrame)
		}
		C.avcodec_free_context(&codecCtx)
		C.avformat_close_input(&fmtCtx)
		return nil, fmt.Errorf("decoder: frame allocation failed")
	}

	fps := stream.avg_frame_rate
	if fps.num <= 0 || fps.den <= 0 {
		fps = stream.r_frame_rate
	}
	if fps.num <= 0 || fps.den <= 0 {
		fps = par.framerate
	}
	tsMul, tsDiv := tsFactors(stream.time_base, fps)
	startTime := int64(0)
	if stream.start_time != C.AV_NOPTS_VALUE {
		startTime = int64(stream.start_time)
	}

	src := &Source{
		fmtCtx:          fmtCtx,
		codecCtx:        codecCtx,
		pkt:             pkt,
		frame:           frame,
		lastFrame:       lastFrame,
		pendingFrame:    pendingFrame,
		filterDesc:      filterDesc,
		lastFilteredIdx: noFilteredFrame,
		streamIdx:       int(streamIdx),
		startTime:       startTime,
		tsMul:           tsMul,
		tsDiv:           tsDiv,
		fpsNum:          int(fps.num),
		fpsDen:          int(fps.den),
	}
	if err := src.anchorFrameOrigin(); err != nil {
		src.Close()
		return nil, err
	}
	return src, nil
}

// Close releases decoder resources.
func (s *Source) Close() {
	if s == nil {
		return
	}
	s.freeFilter()
	if s.filterFrame != nil {
		C.av_frame_free(&s.filterFrame)
	}
	if s.swsCtx != nil {
		C.sws_freeContext(s.swsCtx)
		s.swsCtx = nil
	}
	if s.convFrame != nil {
		C.av_frame_free(&s.convFrame)
	}
	if s.frame != nil {
		C.av_frame_free(&s.frame)
	}
	if s.lastFrame != nil {
		C.av_frame_free(&s.lastFrame)
	}
	if s.pendingFrame != nil {
		C.av_frame_free(&s.pendingFrame)
	}
	if s.pkt != nil {
		C.av_packet_free(&s.pkt)
	}
	if s.codecCtx != nil {
		C.avcodec_free_context(&s.codecCtx)
	}
	if s.fmtCtx != nil {
		C.avformat_close_input(&s.fmtCtx)
	}
}

// ReadFrame extracts a single frame as 10-bit planar little-endian YUV420.
// 8-bit sources are converted to 10-bit by left-shifting by 2.
func (s *Source) ReadFrame(frameIdx int, output []byte, inf *Info, crop *CropRect) error {
	if s == nil {
		return fmt.Errorf("nil video source")
	}
	if inf == nil {
		return fmt.Errorf("nil video info")
	}

	frame, is10Bit, err := s.readFrame(frameIdx, inf)
	if err != nil {
		return err
	}
	planes, linesizes, err := framePlanes(frame, inf.Height)
	if err != nil {
		return err
	}
	return copyFrameTo10Bit(output, planes, linesizes, inf, cropCalcForRect(inf, crop), is10Bit)
}

// FrameReader supplies a chunk's post-crop 10-bit YUV420 frames by absolute
// source frame index. It exists so the encoder and the quality metric can read
// the same frames either straight from a decoder or from a materialized cache
// of already-decoded, already-filtered frames.
type FrameReader interface {
	ReadFrame(frameIdx int, output []byte) error
}

// FrameReader returns a FrameReader that reads this source with a fixed Info
// and crop rectangle.
func (s *Source) FrameReader(inf *Info, crop *CropRect) FrameReader {
	return sourceFrameReader{src: s, inf: inf, crop: crop}
}

type sourceFrameReader struct {
	src  *Source
	inf  *Info
	crop *CropRect
}

func (r sourceFrameReader) ReadFrame(frameIdx int, output []byte) error {
	return r.src.ReadFrame(frameIdx, output, r.inf, r.crop)
}

// ReadLumaFrame retrieves a borrowed view of a frame's luma plane.
func (s *Source) ReadLumaFrame(frameIdx int, inf *Info) (*LumaFrame, error) {
	if s == nil {
		return nil, fmt.Errorf("nil video source")
	}
	if inf == nil {
		return nil, fmt.Errorf("nil video info")
	}

	frame, err := s.readRawFrame(frameIdx)
	if err != nil {
		return nil, err
	}
	return s.lumaFrameFromFrame(frameIdx, frame, inf)
}

// ReadLumaFrameNear retrieves a luma frame near frameIdx without requiring exact frame access.
// It is intended for sampling tasks where an approximate frame is good enough and avoids
// unbounded decode if a container seek lands far before the requested frame.
func (s *Source) ReadLumaFrameNear(frameIdx int, inf *Info, maxDecode int) (*LumaFrame, error) {
	if s == nil {
		return nil, fmt.Errorf("nil video source")
	}
	if inf == nil {
		return nil, fmt.Errorf("nil video info")
	}

	frame, err := s.readRawFrameNear(frameIdx, maxDecode)
	if err != nil {
		return nil, err
	}
	return s.lumaFrameFromFrame(frameIdx, frame, inf)
}

func (s *Source) lumaFrameFromFrame(frameIdx int, frame *C.AVFrame, inf *Info) (*LumaFrame, error) {
	switch int(frame.format) {
	case C.AV_PIX_FMT_YUV420P, C.AV_PIX_FMT_NV12:
		return lumaFrameFromAVFrame(frameIdx, frame, inf, false, 0)
	case C.AV_PIX_FMT_YUV420P10LE:
		return lumaFrameFromAVFrame(frameIdx, frame, inf, true, 2)
	case C.AV_PIX_FMT_P010LE, C.AV_PIX_FMT_P016LE:
		return lumaFrameFromAVFrame(frameIdx, frame, inf, true, 8)
	}

	converted, is10Bit, err := s.normalizeFrame(frame)
	if err != nil {
		return nil, err
	}
	shift := 0
	if is10Bit {
		shift = 2
	}
	return lumaFrameFromAVFrame(frameIdx, converted, inf, is10Bit, shift)
}

func lumaFrameFromAVFrame(frameIdx int, frame *C.AVFrame, inf *Info, is10Bit bool, lumaShift int) (*LumaFrame, error) {
	if frame.data[0] == nil {
		return nil, fmt.Errorf("frame %d has nil luma data", frameIdx)
	}
	stride := int(frame.linesize[0])
	height := int(inf.Height)
	if stride <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid luma geometry for frame %d: stride=%d height=%d", frameIdx, stride, height)
	}
	return &LumaFrame{
		Data:      unsafe.Slice((*byte)(unsafe.Pointer(frame.data[0])), stride*height),
		Stride:    stride,
		Width:     int(inf.Width),
		Height:    height,
		Is10Bit:   is10Bit,
		LumaShift: lumaShift,
	}, nil
}

func (s *Source) readFrame(frameIdx int, _ *Info) (*C.AVFrame, bool, error) {
	frame, err := s.readRawFrame(frameIdx)
	if err != nil {
		return nil, false, err
	}
	// Filter the full decoded frame before any crop: the crop copy in
	// copyFrameTo10Bit stays untouched, and a denoiser sees the same pixels it
	// would see standing alone in front of the encoder.
	filtered, err := s.applyFilter(frameIdx, frame)
	if err != nil {
		return nil, false, err
	}
	return s.normalizeFrame(filtered)
}

// ResetFilter drops temporal denoise-filter state so the next ReadFrame starts
// a fresh filter history. The encoder worker keeps one Source across
// consecutive chunks while the scorers open a fresh Source per chunk, so
// without an explicit reset the same frame would reach the encoder with a
// different filter history than the metric reference. Both paths call this at
// chunk start to keep the two bit-identical. No-op when no filter is set.
func (s *Source) ResetFilter() {
	if s == nil {
		return
	}
	s.freeFilter()
}

// applyFilter runs frame through the denoise graph. The graph is rebuilt on a
// non-sequential read so filter history always starts at the first frame of a
// sequential run.
func (s *Source) applyFilter(frameIdx int, frame *C.AVFrame) (*C.AVFrame, error) {
	if s.filterDesc == "" {
		return frame, nil
	}
	if s.filterGraph == nil || frameIdx != s.lastFilteredIdx+1 ||
		s.filterFormat != int(frame.format) || s.filterWidth != frame.width || s.filterHeight != frame.height {
		if err := s.initFilter(frame); err != nil {
			return nil, err
		}
	}

	C.av_frame_unref(s.filterFrame)
	ret := C.reel_filter_frame(s.filterSrcCtx, s.filterSinkCtx, frame, s.filterFrame, C.int64_t(frameIdx))
	if ret == C.reel_averror_eagain() {
		return nil, fmt.Errorf("denoise filter %q delays output; only filters that emit one frame per input frame are supported", s.filterDesc)
	}
	if ret < 0 {
		return nil, fmt.Errorf("denoise filter failed on frame %d: %s", frameIdx, avError(ret))
	}
	if int(s.filterFrame.format) != s.filterFormat {
		return nil, fmt.Errorf("denoise filter changed pixel format from %d to %d", s.filterFormat, int(s.filterFrame.format))
	}
	s.lastFilteredIdx = frameIdx
	return s.filterFrame, nil
}

func (s *Source) initFilter(frame *C.AVFrame) error {
	s.freeFilter()
	if s.filterFrame == nil {
		s.filterFrame = C.av_frame_alloc()
		if s.filterFrame == nil {
			return fmt.Errorf("denoise: frame allocation failed")
		}
	}

	pixName := C.GoString(C.reel_pix_fmt_name(C.int(frame.format)))
	if pixName == "" {
		return fmt.Errorf("denoise: unnamed decoder pixel format %d", int(frame.format))
	}
	// Pin the graph output to the decoder's own pixel format so no filter in the
	// chain can quietly insert a conversion; a format change here would alter
	// both the encoder input and the metric reference.
	desc := s.filterDesc + ",format=" + pixName
	cDesc := C.CString(desc)
	defer C.free(unsafe.Pointer(cDesc))

	// Frames are pushed with pts = frame index, so one tick is one frame.
	tbNum, tbDen := s.fpsDen, s.fpsNum
	if tbNum <= 0 || tbDen <= 0 {
		tbNum, tbDen = 1, 25
	}
	sarNum, sarDen := 1, 1
	if frame.sample_aspect_ratio.num > 0 && frame.sample_aspect_ratio.den > 0 {
		sarNum = int(frame.sample_aspect_ratio.num)
		sarDen = int(frame.sample_aspect_ratio.den)
	}

	ret := C.reel_filter_init(&s.filterGraph, &s.filterSrcCtx, &s.filterSinkCtx, cDesc,
		frame.width, frame.height, frame.format,
		C.int(tbNum), C.int(tbDen), C.int(sarNum), C.int(sarDen))
	if ret < 0 {
		s.filterGraph = nil
		s.filterSrcCtx = nil
		s.filterSinkCtx = nil
		return fmt.Errorf("failed to build denoise filter %q: %s", desc, avError(ret))
	}
	s.filterFormat = int(frame.format)
	s.filterWidth = frame.width
	s.filterHeight = frame.height
	return nil
}

func (s *Source) freeFilter() {
	if s.filterGraph != nil {
		C.avfilter_graph_free(&s.filterGraph)
	}
	s.filterGraph = nil
	s.filterSrcCtx = nil
	s.filterSinkCtx = nil
	s.lastFilteredIdx = noFilteredFrame
	if s.filterFrame != nil {
		C.av_frame_unref(s.filterFrame)
	}
}

func (s *Source) readRawFrame(frameIdx int) (*C.AVFrame, error) {
	if frameIdx < 0 {
		return nil, fmt.Errorf("negative frame index %d", frameIdx)
	}
	pendingCoversRequest := s.hasPending && frameIdx <= s.pendingIdx
	if (frameIdx < s.nextFrame && !pendingCoversRequest) || frameIdx-s.nextFrame > 150 {
		if err := s.seekNear(frameIdx); err != nil {
			return nil, err
		}
	}

	for {
		if s.hasPending {
			switch {
			case s.pendingIdx < frameIdx:
				if err := s.replaceLastFrame(s.pendingFrame); err != nil {
					return nil, err
				}
				s.hasPending = false
				C.av_frame_unref(s.pendingFrame)
				continue
			case s.pendingIdx == frameIdx:
				if err := s.replaceLastFrame(s.pendingFrame); err != nil {
					return nil, err
				}
				s.hasPending = false
				C.av_frame_unref(s.pendingFrame)
				return s.lastFrame, nil
			default:
				if s.hasLast {
					return s.lastFrame, nil
				}
				return s.pendingFrame, nil
			}
		}

		frame, decodedIdx, err := s.decodeOne()
		if err != nil {
			return nil, fmt.Errorf("failed to decode frame %d: %w", frameIdx, err)
		}
		if decodedIdx < frameIdx {
			if err := s.replaceLastFrame(frame); err != nil {
				return nil, err
			}
			continue
		}
		if decodedIdx == frameIdx {
			if err := s.replaceLastFrame(frame); err != nil {
				return nil, err
			}
			return s.lastFrame, nil
		}

		// Soft-telecined and other variable-cadence sources can leave holes in
		// the nominal CFR timeline. Repeat the prior frame until this frame's
		// timestamp slot instead of rejecting valid input or shortening it.
		C.av_frame_unref(s.pendingFrame)
		if ret := C.av_frame_ref(s.pendingFrame, frame); ret < 0 {
			return nil, fmt.Errorf("buffer future frame %d: %s", decodedIdx, avError(ret))
		}
		s.hasPending = true
		s.pendingIdx = decodedIdx
		if s.hasLast {
			return s.lastFrame, nil
		}
		return s.pendingFrame, nil
	}
}

func (s *Source) replaceLastFrame(frame *C.AVFrame) error {
	C.av_frame_unref(s.lastFrame)
	if ret := C.av_frame_ref(s.lastFrame, frame); ret < 0 {
		s.hasLast = false
		return fmt.Errorf("buffer decoded frame: %s", avError(ret))
	}
	s.hasLast = true
	return nil
}

func (s *Source) readFrameNear(frameIdx int, maxDecode int) (*C.AVFrame, bool, error) {
	frame, err := s.readRawFrameNear(frameIdx, maxDecode)
	if err != nil {
		return nil, false, err
	}
	return s.normalizeFrame(frame)
}

func (s *Source) readRawFrameNear(frameIdx int, maxDecode int) (*C.AVFrame, error) {
	if frameIdx < 0 {
		return nil, fmt.Errorf("negative frame index %d", frameIdx)
	}
	if maxDecode < 1 {
		maxDecode = 1
	}
	if err := s.seekNear(frameIdx); err != nil {
		return nil, err
	}

	var frame *C.AVFrame
	for decoded := 0; decoded < maxDecode; decoded++ {
		decodedFrame, decodedIdx, err := s.decodeOne()
		if err != nil {
			return nil, fmt.Errorf("failed to decode near frame %d: %w", frameIdx, err)
		}
		frame = decodedFrame
		if decodedIdx >= frameIdx {
			break
		}
	}
	if frame == nil {
		return nil, fmt.Errorf("failed to decode near frame %d", frameIdx)
	}
	return frame, nil
}

func (s *Source) anchorFrameOrigin() error {
	// Some containers report the video stream start before the first displayable frame
	// (for example VC-1 in Matroska with an initial decoder-delay frame). Anchor
	// frame zero to the first decoded frame so timestamp-derived indexes stay exact.
	frame, _, err := s.decodeOne()
	if err != nil {
		if err == io.EOF {
			return s.seekNear(0)
		}
		return fmt.Errorf("decoder: decode first frame failed: %w", err)
	}
	if frame.best_effort_timestamp != C.AV_NOPTS_VALUE {
		s.startTime = int64(frame.best_effort_timestamp)
	}
	return s.seekNear(0)
}

func (s *Source) seekNear(frameIdx int) error {
	if s.tsDiv == 0 {
		return fmt.Errorf("invalid stream time base")
	}
	// Seek slightly before the target frame. Some demuxers seek by DTS/keyframe,
	// so seeking to an exact display timestamp can resume after the requested
	// frame when B-frames or decoder-delay frames are present.
	seekFrame := max(0, frameIdx-seekPrerollFrames)
	ts := s.startTime + int64(seekFrame)*s.tsMul/s.tsDiv
	if ret := C.av_seek_frame(s.fmtCtx, C.int(s.streamIdx), C.int64_t(ts), C.AVSEEK_FLAG_BACKWARD); ret < 0 {
		return fmt.Errorf("seek to frame %d failed: %s", frameIdx, avError(ret))
	}
	C.avcodec_flush_buffers(s.codecCtx)
	C.av_frame_unref(s.lastFrame)
	C.av_frame_unref(s.pendingFrame)
	s.eof = false
	s.hasLast = false
	s.hasPending = false
	s.nextFrame = seekFrame
	return nil
}

func (s *Source) decodeOne() (*C.AVFrame, int, error) {
	if s.eof {
		return nil, 0, io.EOF
	}
	for {
		ret := C.avcodec_receive_frame(s.codecCtx, s.frame)
		if ret == 0 {
			idx := s.frameIndex(s.frame)
			s.nextFrame = idx + 1
			return s.frame, idx, nil
		}
		if ret == C.reel_averror_eof() {
			s.eof = true
			return nil, 0, io.EOF
		}
		if ret != C.reel_averror_eagain() {
			return nil, 0, fmt.Errorf("receive frame failed: %s", avError(ret))
		}

		for {
			readRet := C.av_read_frame(s.fmtCtx, s.pkt)
			if readRet < 0 {
				C.avcodec_send_packet(s.codecCtx, nil)
				break
			}
			if int(s.pkt.stream_index) != s.streamIdx {
				C.av_packet_unref(s.pkt)
				continue
			}
			sendRet := C.avcodec_send_packet(s.codecCtx, s.pkt)
			C.av_packet_unref(s.pkt)
			if sendRet == 0 || sendRet == C.reel_averror_eagain() {
				break
			}
			return nil, 0, fmt.Errorf("send packet failed: %s", avError(sendRet))
		}
	}
}

func (s *Source) frameIndex(frame *C.AVFrame) int {
	pts := int64(frame.best_effort_timestamp)
	if pts == C.AV_NOPTS_VALUE || s.tsMul == 0 {
		return s.nextFrame
	}
	pts -= s.startTime
	if pts < 0 {
		return s.nextFrame
	}
	return int((pts*s.tsDiv + s.tsMul/2) / s.tsMul)
}

func (s *Source) normalizeFrame(frame *C.AVFrame) (*C.AVFrame, bool, error) {
	format := int(frame.format)
	if format == C.AV_PIX_FMT_YUV420P {
		return frame, false, nil
	}
	if format == C.AV_PIX_FMT_YUV420P10LE {
		return frame, true, nil
	}

	if s.convFrame == nil || s.convFrame.width != frame.width || s.convFrame.height != frame.height || s.swsFormat != format {
		if s.swsCtx != nil {
			C.sws_freeContext(s.swsCtx)
			s.swsCtx = nil
		}
		if s.convFrame != nil {
			C.av_frame_free(&s.convFrame)
		}
		s.convFrame = C.av_frame_alloc()
		if s.convFrame == nil {
			return nil, false, fmt.Errorf("failed to allocate conversion frame")
		}
		s.convFrame.format = C.AV_PIX_FMT_YUV420P10LE
		s.convFrame.width = frame.width
		s.convFrame.height = frame.height
	}
	if s.swsCtx == nil {
		s.swsCtx = C.sws_getContext(frame.width, frame.height, C.enum_AVPixelFormat(format), frame.width, frame.height, C.AV_PIX_FMT_YUV420P10LE, C.SWS_BILINEAR, nil, nil, nil)
		if s.swsCtx == nil {
			return nil, false, fmt.Errorf("failed to create pixel format converter")
		}
		s.swsFormat = format
	}
	C.av_frame_unref(s.convFrame)
	s.convFrame.format = C.AV_PIX_FMT_YUV420P10LE
	s.convFrame.width = frame.width
	s.convFrame.height = frame.height
	if ret := C.av_frame_get_buffer(s.convFrame, 32); ret < 0 {
		return nil, false, fmt.Errorf("failed to allocate conversion buffer: %s", avError(ret))
	}
	if ret := C.reel_sws_scale(s.swsCtx, frame, s.convFrame); ret <= 0 {
		return nil, false, fmt.Errorf("pixel format conversion failed")
	}
	return s.convFrame, true, nil
}

func (s *Source) frameCount(stream *C.AVStream, fps C.AVRational) int {
	if stream.nb_frames > 0 {
		return int(stream.nb_frames)
	}
	if frames := framesFromStreamDuration(
		int64(stream.duration),
		int64(stream.time_base.num),
		int64(stream.time_base.den),
		int64(fps.num),
		int64(fps.den),
	); frames > 0 {
		return int(frames)
	}
	if s != nil && s.fmtCtx != nil {
		if frames := framesFromAVDuration(int64(s.fmtCtx.duration), int64(fps.num), int64(fps.den)); frames > 0 {
			return int(frames)
		}
	}
	return s.countVideoPackets()
}

func framesFromStreamDuration(duration, timeBaseNum, timeBaseDen, fpsNum, fpsDen int64) int64 {
	if duration <= 0 || timeBaseNum <= 0 || timeBaseDen <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	return duration * timeBaseNum * fpsNum / (timeBaseDen * fpsDen)
}

func framesFromAVDuration(duration, fpsNum, fpsDen int64) int64 {
	if duration <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	return duration * fpsNum / (avTimeBase * fpsDen)
}

func (s *Source) countVideoPackets() int {
	count := 0
	for C.av_read_frame(s.fmtCtx, s.pkt) >= 0 {
		if int(s.pkt.stream_index) == s.streamIdx {
			count++
		}
		C.av_packet_unref(s.pkt)
	}
	_ = C.av_seek_frame(s.fmtCtx, C.int(s.streamIdx), 0, C.AVSEEK_FLAG_BACKWARD)
	C.avcodec_flush_buffers(s.codecCtx)
	s.eof = false
	s.nextFrame = 0
	return count
}

func (s *Source) fillFrameMetadata(inf *Info, frame *C.AVFrame, par *C.AVCodecParameters) {
	inf.ColorPrimaries = svtColorValue(int(frame.color_primaries), int(par.color_primaries))
	inf.TransferCharacteristics = svtColorValue(int(frame.color_trc), int(par.color_trc))
	inf.MatrixCoefficients = svtColorValue(int(frame.colorspace), int(par.color_space))
	inf.ColorRange = svtColorRange(int(frame.color_range), int(par.color_range))
	inf.ChromaSamplePosition = svtChromaSamplePosition(int(frame.chroma_location), int(par.chroma_location))
	inf.MasteringDisplay = masteringDisplayFromFrame(frame)
	inf.ContentLight = contentLightFromFrame(frame)
}

func pixelDepth(format int) int {
	return int(C.reel_pix_fmt_depth(C.int(format)))
}

func tsFactors(tb, fps C.AVRational) (int64, int64) {
	if tb.num > 0 && tb.den > 0 && fps.num > 0 && fps.den > 0 {
		return int64(tb.den) * int64(fps.den), int64(tb.num) * int64(fps.num)
	}
	return 1, 1
}

func avError(ret C.int) string {
	buf := (*C.char)(C.malloc(256))
	defer C.free(unsafe.Pointer(buf))
	C.reel_strerror(ret, buf, 256)
	return C.GoString(buf)
}

func svtColorValue(avValues ...int) *int32 {
	for _, avValue := range avValues {
		if avValue > 0 && avValue != avColorUnspecified && avValue != avColorReserved {
			v := int32(avValue)
			return &v
		}
	}
	v := int32(avColorBT709)
	return &v
}

func svtColorRange(avRanges ...int) *int32 {
	for _, avRange := range avRanges {
		switch avRange {
		case 1: // AVCOL_RANGE_MPEG, SVT-AV1 studio/limited range
			v := int32(svtColorRangeLimited)
			return &v
		case 2: // AVCOL_RANGE_JPEG, SVT-AV1 full range
			v := int32(svtColorRangeFull)
			return &v
		case 0: // AVCOL_RANGE_UNSPECIFIED
			continue
		default:
			return nil
		}
	}
	v := int32(svtColorRangeLimited)
	return &v
}

func svtChromaSamplePosition(avLocations ...int) *int32 {
	for _, avLocation := range avLocations {
		switch avLocation {
		case 1: // AVCHROMA_LOC_LEFT
			v := int32(svtChromaPositionLeft) // AV1 vertical/left
			return &v
		case 3: // AVCHROMA_LOC_TOPLEFT
			v := int32(svtChromaPositionTopLeft) // AV1 colocated/topleft
			return &v
		case 0: // AVCHROMA_LOC_UNSPECIFIED
			continue
		default:
			return nil
		}
	}
	v := int32(svtChromaPositionLeft)
	return &v
}

func masteringDisplayFromFrame(frame *C.AVFrame) *string {
	sideData := C.av_frame_get_side_data(frame, C.AV_FRAME_DATA_MASTERING_DISPLAY_METADATA)
	if sideData == nil {
		return nil
	}
	md := (*C.AVMasteringDisplayMetadata)(unsafe.Pointer(sideData.data))
	if md.has_primaries == 0 || md.has_luminance == 0 {
		return nil
	}
	return formatMasteringDisplay(
		ratToFloat64(md.display_primaries[0][0]),
		ratToFloat64(md.display_primaries[0][1]),
		ratToFloat64(md.display_primaries[1][0]),
		ratToFloat64(md.display_primaries[1][1]),
		ratToFloat64(md.display_primaries[2][0]),
		ratToFloat64(md.display_primaries[2][1]),
		ratToFloat64(md.white_point[0]),
		ratToFloat64(md.white_point[1]),
		ratToFloat64(md.max_luminance),
		ratToFloat64(md.min_luminance),
	)
}

func contentLightFromFrame(frame *C.AVFrame) *string {
	sideData := C.av_frame_get_side_data(frame, C.AV_FRAME_DATA_CONTENT_LIGHT_LEVEL)
	if sideData == nil {
		return nil
	}
	cl := (*C.AVContentLightMetadata)(unsafe.Pointer(sideData.data))
	return formatContentLight(uint32(cl.MaxCLL), uint32(cl.MaxFALL))
}

func ratToFloat64(r C.AVRational) float64 {
	if r.den == 0 {
		return 0
	}
	return float64(r.num) / float64(r.den)
}

func formatMasteringDisplay(rx, ry, gx, gy, bx, by, wpx, wpy, maxLum, minLum float64) *string {
	// SVT-AV1 expects G, B, R ordering in the mastering-display string.
	md := fmt.Sprintf(
		"G(%.4f,%.4f)B(%.4f,%.4f)R(%.4f,%.4f)WP(%.4f,%.4f)L(%.4f,%.4f)",
		gx, gy,
		bx, by,
		rx, ry,
		wpx, wpy,
		maxLum, minLum,
	)
	return &md
}

func formatContentLight(maxCLL, maxFALL uint32) *string {
	cl := fmt.Sprintf("%d,%d", maxCLL, maxFALL)
	return &cl
}

func cropCalcForRect(inf *Info, rect *CropRect) *CropCalc {
	if inf == nil || rect == nil || (rect.X == 0 && rect.Y == 0 && rect.Width == inf.Width && rect.Height == inf.Height) {
		return nil
	}
	return &CropCalc{NewW: rect.Width, NewH: rect.Height, CropX: rect.X, CropY: rect.Y}
}

// ValidateCropRect validates a YUV420 crop rectangle.
func ValidateCropRect(inf *Info, rect CropRect) error {
	if inf == nil {
		return fmt.Errorf("nil video info")
	}
	if rect.Width == 0 || rect.Height == 0 {
		return fmt.Errorf("invalid crop rectangle: width and height must be non-zero")
	}
	if rect.X > inf.Width || rect.Width > inf.Width-rect.X || rect.Y > inf.Height || rect.Height > inf.Height-rect.Y {
		return fmt.Errorf("invalid crop rectangle %dx%d+%d+%d for source %dx%d", rect.Width, rect.Height, rect.X, rect.Y, inf.Width, inf.Height)
	}
	if rect.X%2 != 0 || rect.Y%2 != 0 || rect.Width%2 != 0 || rect.Height%2 != 0 {
		return fmt.Errorf("invalid crop rectangle %dx%d+%d+%d: YUV420 crop offsets and dimensions must be even", rect.Width, rect.Height, rect.X, rect.Y)
	}
	return nil
}

func framePlanes(frame *C.AVFrame, sourceHeight uint32) ([3][]byte, [3]int, error) {
	if frame.data[0] == nil || frame.data[1] == nil || frame.data[2] == nil {
		return [3][]byte{}, [3]int{}, fmt.Errorf("frame has nil plane data")
	}
	height := int(sourceHeight)
	planes := [3][]byte{
		unsafe.Slice((*byte)(unsafe.Pointer(frame.data[0])), int(frame.linesize[0])*height),
		unsafe.Slice((*byte)(unsafe.Pointer(frame.data[1])), int(frame.linesize[1])*height/2),
		unsafe.Slice((*byte)(unsafe.Pointer(frame.data[2])), int(frame.linesize[2])*height/2),
	}
	linesizes := [3]int{int(frame.linesize[0]), int(frame.linesize[1]), int(frame.linesize[2])}
	return planes, linesizes, nil
}

func copyFrameTo10Bit(output []byte, planes [3][]byte, linesizes [3]int, inf *Info, cropCalc *CropCalc, sourceIs10Bit bool) error {
	width := inf.Width
	height := inf.Height
	cropX := uint32(0)
	cropY := uint32(0)
	if cropCalc != nil {
		width = cropCalc.NewW
		height = cropCalc.NewH
		cropX = cropCalc.CropX
		cropY = cropCalc.CropY
	}

	yPlaneSize := int(width) * int(height) * 2
	uPlaneSize := int(width) * int(height) / 4 * 2
	vPlaneSize := int(width) * int(height) / 4 * 2
	expectedSize := yPlaneSize + uPlaneSize + vPlaneSize
	if len(output) < expectedSize {
		return fmt.Errorf("output buffer too small: need %d, got %d", expectedSize, len(output))
	}

	if sourceIs10Bit {
		bytesPerPixel := 2
		yStart := int(cropY)*linesizes[0] + int(cropX)*bytesPerPixel
		uStart := int(cropY/2)*linesizes[1] + int(cropX/2)*bytesPerPixel
		vStart := int(cropY/2)*linesizes[2] + int(cropX/2)*bytesPerPixel
		yRowLen := int(width) * bytesPerPixel
		uvRowLen := int(width/2) * bytesPerPixel

		if err := validatePlaneBounds("Y", planes[0], yStart, int(height), yRowLen, linesizes[0]); err != nil {
			return err
		}
		if err := validatePlaneBounds("U", planes[1], uStart, int(height/2), uvRowLen, linesizes[1]); err != nil {
			return err
		}
		if err := validatePlaneBounds("V", planes[2], vStart, int(height/2), uvRowLen, linesizes[2]); err != nil {
			return err
		}

		copyPlane10bit(output[:yPlaneSize], planes[0][yStart:], int(height), yRowLen, linesizes[0])
		copyPlane10bit(output[yPlaneSize:yPlaneSize+uPlaneSize], planes[1][uStart:], int(height/2), uvRowLen, linesizes[1])
		copyPlane10bit(output[yPlaneSize+uPlaneSize:], planes[2][vStart:], int(height/2), uvRowLen, linesizes[2])
		return nil
	}

	// Source is 8-bit, convert to 10-bit (left shift by 2).
	yStart := int(cropY)*linesizes[0] + int(cropX)
	uStart := int(cropY/2)*linesizes[1] + int(cropX/2)
	vStart := int(cropY/2)*linesizes[2] + int(cropX/2)
	yRowLen := int(width)
	uvRowLen := int(width / 2)

	if err := validatePlaneBounds("Y", planes[0], yStart, int(height), yRowLen, linesizes[0]); err != nil {
		return err
	}
	if err := validatePlaneBounds("U", planes[1], uStart, int(height/2), uvRowLen, linesizes[1]); err != nil {
		return err
	}
	if err := validatePlaneBounds("V", planes[2], vStart, int(height/2), uvRowLen, linesizes[2]); err != nil {
		return err
	}

	convert8to10bit(output[:yPlaneSize], planes[0][yStart:], int(width), int(height), linesizes[0])
	convert8to10bit(output[yPlaneSize:yPlaneSize+uPlaneSize], planes[1][uStart:], int(width/2), int(height/2), linesizes[1])
	convert8to10bit(output[yPlaneSize+uPlaneSize:], planes[2][vStart:], int(width/2), int(height/2), linesizes[2])
	return nil
}

func validatePlaneBounds(name string, plane []byte, start, rows, rowLen, stride int) error {
	if start < 0 || rows < 0 || rowLen < 0 || stride < rowLen {
		return fmt.Errorf("invalid %s plane geometry: start=%d rows=%d rowLen=%d stride=%d", name, start, rows, rowLen, stride)
	}
	if rows == 0 {
		if start > len(plane) {
			return fmt.Errorf("%s plane crop starts beyond plane data: start=%d len=%d", name, start, len(plane))
		}
		return nil
	}
	end := start + (rows-1)*stride + rowLen
	if end > len(plane) {
		return fmt.Errorf("%s plane crop exceeds plane data: end=%d len=%d", name, end, len(plane))
	}
	return nil
}

// copyPlane10bit copies a 10-bit plane handling stride differences.
func copyPlane10bit(dst, src []byte, rows, dstStride, srcStride int) {
	srcOff := 0
	dstOff := 0
	copyLen := dstStride
	if srcStride < dstStride {
		copyLen = srcStride
	}
	for range rows {
		copy(dst[dstOff:dstOff+copyLen], src[srcOff:srcOff+copyLen])
		srcOff += srcStride
		dstOff += dstStride
	}
}

// convert8to10bit converts 8-bit YUV data to 10-bit by left-shifting by 2.
func convert8to10bit(dst, src []byte, width, height, srcStride int) {
	dstOff := 0
	for row := range height {
		srcRowStart := row * srcStride
		for col := range width {
			sample8 := uint16(src[srcRowStart+col])
			sample10 := sample8 << 2
			binary.LittleEndian.PutUint16(dst[dstOff:], sample10)
			dstOff += 2
		}
	}
}

// FrameSize returns the buffer size needed for a frame. Output is always 10-bit YUV420.
func FrameSize(inf *Info, crop *CropRect) int {
	w, h := OutputDimensions(inf, crop)
	return Calc10BitSize(w, h)
}

// OutputDimensions returns encoded frame dimensions after crop.
func OutputDimensions(inf *Info, crop *CropRect) (uint32, uint32) {
	if inf == nil {
		return 0, 0
	}
	if crop != nil {
		return crop.Width, crop.Height
	}
	return inf.Width, inf.Height
}

// Calc10BitSize calculates the buffer size for planar 10-bit YUV420.
func Calc10BitSize(w, h uint32) int {
	return int(w) * int(h) * 3
}

// Calc8BitSize calculates the buffer size for planar 8-bit YUV420.
func Calc8BitSize(w, h uint32) int {
	return int(w) * int(h) * 3 / 2
}
