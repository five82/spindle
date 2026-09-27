package chunk

/*
#cgo pkg-config: libavformat libavcodec libavutil
#include <libavformat/avformat.h>
#include <libavcodec/packet.h>
#include <libavutil/avutil.h>
#include <libavutil/dict.h>
#include <libavutil/error.h>
#include <libavutil/log.h>
#include <libavutil/mathematics.h>
#include <libavutil/mem.h>
#include <libavutil/rational.h>
#include <limits.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static void reel_mux_init_av_log(void) {
	av_log_set_level(AV_LOG_ERROR);
}

static void reel_mux_set_probe_size(AVFormatContext *ctx) {
	ctx->probesize = 0x80000;
}

static AVStream* reel_mux_stream_at(AVFormatContext *ctx, int idx) {
	return ctx->streams[idx];
}

static AVRational reel_mux_stream_time_base(AVFormatContext *ctx, int idx) {
	return ctx->streams[idx]->time_base;
}

static int reel_mux_stream_index(AVFormatContext *ctx, int idx) {
	return ctx->streams[idx]->index;
}

static int reel_mux_best_stream(AVFormatContext *ctx, enum AVMediaType media_type) {
	return av_find_best_stream(ctx, media_type, -1, -1, NULL, 0);
}

static int reel_mux_add_stream_copy(AVFormatContext *out, AVStream *src) {
	AVStream *dst = avformat_new_stream(out, NULL);
	if (dst == NULL) {
		return AVERROR(ENOMEM);
	}
	int ret = avcodec_parameters_copy(dst->codecpar, src->codecpar);
	if (ret < 0) {
		return ret;
	}
	dst->time_base = src->time_base;
	dst->avg_frame_rate = src->avg_frame_rate;
	dst->r_frame_rate = src->r_frame_rate;
	dst->disposition = src->disposition;
	return dst->index;
}

static int reel_mux_alloc_output(AVFormatContext **out, const char *path) {
	return avformat_alloc_output_context2(out, NULL, NULL, path);
}

static int reel_mux_needs_avio(AVFormatContext *ctx) {
	return !(ctx->oformat->flags & AVFMT_NOFILE);
}

static int reel_mux_open_avio(AVFormatContext *ctx, const char *path) {
	return avio_open(&ctx->pb, path, AVIO_FLAG_WRITE);
}

static int reel_mux_close_avio(AVFormatContext *ctx) {
	return avio_closep(&ctx->pb);
}

static int reel_mux_write_header(AVFormatContext *ctx) {
	AVDictionary *opts = NULL;
	if (ctx->oformat != NULL && ctx->oformat->name != NULL) {
		const char *name = ctx->oformat->name;
		if (strstr(name, "mp4") != NULL || strstr(name, "mov") != NULL) {
			av_dict_set(&opts, "movflags", "+faststart", 0);
		}
		if (strstr(name, "matroska") != NULL || strstr(name, "webm") != NULL) {
			av_dict_set(&opts, "write_crc32", "0", 0);
		}
	}
	int ret = avformat_write_header(ctx, &opts);
	av_dict_free(&opts);
	return ret;
}

static void reel_mux_copy_global_metadata(AVFormatContext *out, AVFormatContext *src) {
	av_dict_copy(&out->metadata, src->metadata, 0);
}

static int reel_mux_clamp_chapter(int64_t start, int64_t *chapter_end, int64_t video_end) {
	if (start >= video_end) {
		return 0;
	}
	if (*chapter_end > video_end) {
		*chapter_end = video_end;
	}
	return 1;
}

static int reel_mux_copy_chapters(AVFormatContext *out, AVFormatContext *src, int64_t video_end_us) {
	if (src->nb_chapters == 0) {
		return 0;
	}
	AVChapter **chapters = av_calloc(src->nb_chapters, sizeof(*chapters));
	if (chapters == NULL) {
		return AVERROR(ENOMEM);
	}
	unsigned int copied = 0;
	for (unsigned int i = 0; i < src->nb_chapters; i++) {
		AVChapter *in = src->chapters[i];
		int64_t end = av_rescale_q_rnd(video_end_us, (AVRational){1, 1000000}, in->time_base, AV_ROUND_DOWN);
		int64_t chapter_end = in->end;
		if (!reel_mux_clamp_chapter(in->start, &chapter_end, end)) {
			continue;
		}
		AVChapter *chapter = av_mallocz(sizeof(*chapter));
		if (chapter == NULL) {
			for (unsigned int j = 0; j < copied; j++) {
				av_dict_free(&chapters[j]->metadata);
				av_free(chapters[j]);
			}
			av_free(chapters);
			return AVERROR(ENOMEM);
		}
		chapter->id = in->id;
		chapter->time_base = in->time_base;
		chapter->start = in->start;
		chapter->end = chapter_end;
		av_dict_copy(&chapter->metadata, in->metadata, 0);
		chapters[copied++] = chapter;
	}
	if (copied == 0) {
		av_free(chapters);
		return 0;
	}
	out->chapters = chapters;
	out->nb_chapters = copied;
	return 0;
}

static void reel_mux_set_metadata(AVFormatContext *ctx, int stream_idx, const char *key, const char *value) {
	if (value == NULL || value[0] == '\0') {
		return;
	}
	AVStream *stream = ctx->streams[stream_idx];
	av_dict_set(&stream->metadata, key, value, 0);
}

static void reel_mux_set_disposition(AVFormatContext *ctx, int stream_idx, int disposition) {
	ctx->streams[stream_idx]->disposition = disposition;
}

static void reel_mux_set_display_aspect(AVFormatContext *ctx, int stream_idx, int dar_num, int dar_den) {
	if (dar_num <= 0 || dar_den <= 0) {
		return;
	}
	AVStream *stream = ctx->streams[stream_idx];
	AVCodecParameters *par = stream->codecpar;
	if (par->width <= 0 || par->height <= 0) {
		return;
	}
	AVRational sar = {0, 1};
	av_reduce(&sar.num, &sar.den,
		(int64_t)dar_num * par->height,
		(int64_t)dar_den * par->width,
		INT_MAX);
	par->sample_aspect_ratio = sar;
	stream->sample_aspect_ratio = sar;
}

static int64_t reel_mux_packet_time_us(AVPacket *pkt, AVRational tb) {
	int64_t ts = pkt->dts == AV_NOPTS_VALUE ? pkt->pts : pkt->dts;
	if (ts == AV_NOPTS_VALUE) {
		return 0;
	}
	return av_rescale_q(ts, tb, (AVRational){1, 1000000});
}

static void reel_mux_packet_set_stream_index(AVPacket *pkt, int stream_idx) {
	pkt->stream_index = stream_idx;
}

static void reel_mux_packet_shift_ts(AVPacket *pkt, int64_t offset_us, AVRational time_base) {
	if (offset_us == 0) {
		return;
	}
	int64_t offset = av_rescale_q(offset_us, (AVRational){1, 1000000}, time_base);
	if (pkt->pts != AV_NOPTS_VALUE) {
		pkt->pts += offset;
	}
	if (pkt->dts != AV_NOPTS_VALUE) {
		pkt->dts += offset;
	}
}

static char* reel_mux_error_string(int errnum) {
	char buf[AV_ERROR_MAX_STRING_SIZE];
	av_strerror(errnum, buf, sizeof(buf));
	return av_strdup(buf);
}
*/
import "C"

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	nativeaudio "github.com/five82/reel/internal/audio"
	"github.com/five82/reel/internal/media"
)

const (
	avDispositionDefault         = int(C.AV_DISPOSITION_DEFAULT)
	avDispositionDub             = int(C.AV_DISPOSITION_DUB)
	avDispositionOriginal        = int(C.AV_DISPOSITION_ORIGINAL)
	avDispositionComment         = int(C.AV_DISPOSITION_COMMENT)
	avDispositionLyrics          = int(C.AV_DISPOSITION_LYRICS)
	avDispositionKaraoke         = int(C.AV_DISPOSITION_KARAOKE)
	avDispositionForced          = int(C.AV_DISPOSITION_FORCED)
	avDispositionHearingImpaired = int(C.AV_DISPOSITION_HEARING_IMPAIRED)
	avDispositionVisualImpaired  = int(C.AV_DISPOSITION_VISUAL_IMPAIRED)
	avDispositionCleanEffects    = int(C.AV_DISPOSITION_CLEAN_EFFECTS)
	avDispositionAttachedPic     = int(C.AV_DISPOSITION_ATTACHED_PIC)
	avDispositionTimedThumbnails = int(C.AV_DISPOSITION_TIMED_THUMBNAILS)
)

var muxInitOnce sync.Once

func initMuxLibav() {
	muxInitOnce.Do(func() {
		C.reel_mux_init_av_log()
	})
}

// MuxFinal combines the encoded video with audio, chapters, and metadata using libavformat.
func MuxFinal(inputPath, workDir, outputPath string, audioStreams []nativeaudio.EncodedStream, displayAspect string, videoDurationSecs float64) error {
	initMuxLibav()

	videoPath := GetVideoPath(workDir)
	if _, err := os.Stat(videoPath); err != nil {
		return fmt.Errorf("video file not found: %w", err)
	}
	for _, stream := range audioStreams {
		if _, err := os.Stat(stream.Path); err != nil {
			return fmt.Errorf("audio file not found: %w", err)
		}
	}

	muxer, err := newNativeMuxer(outputPath)
	if err != nil {
		return err
	}
	defer muxer.close()

	if err := muxer.openSource(inputPath); err != nil {
		return err
	}
	if err := muxer.addVideo(videoPath, displayAspect); err != nil {
		return err
	}
	for _, stream := range audioStreams {
		if err := muxer.addAudio(stream); err != nil {
			return err
		}
	}
	if err := muxer.copySourceMetadata(offsetMicroseconds(videoDurationSecs)); err != nil {
		return err
	}
	if err := muxer.write(outputPath); err != nil {
		return err
	}
	return nil
}

type nativeMuxer struct {
	out        *C.AVFormatContext
	source     *C.AVFormatContext
	inputs     []*C.AVFormatContext
	feeds      []*muxFeed
	openedAVIO bool
}

type muxFeed struct {
	ctx         *C.AVFormatContext
	packet      *C.AVPacket
	inputIndex  C.int
	outputIndex C.int
	timeBase    C.AVRational
	offsetUS    int64
	hasPacket   bool
	done        bool
	timeUS      int64
}

func newNativeMuxer(outputPath string) (*nativeMuxer, error) {
	cPath := C.CString(outputPath)
	defer C.free(unsafe.Pointer(cPath))

	var out *C.AVFormatContext
	if ret := C.reel_mux_alloc_output(&out, cPath); ret < 0 || out == nil {
		return nil, fmt.Errorf("could not create output container: %s", avError(ret))
	}
	return &nativeMuxer{out: out}, nil
}

func (m *nativeMuxer) openSource(path string) error {
	ctx, err := openMuxInput(path)
	if err != nil {
		return fmt.Errorf("open source for metadata: %w", err)
	}
	m.source = ctx
	return nil
}

func (m *nativeMuxer) addVideo(path, displayAspect string) error {
	ctx, streamPos, err := openMuxInputBest(path, C.AVMEDIA_TYPE_VIDEO)
	if err != nil {
		return fmt.Errorf("open encoded video: %w", err)
	}
	m.inputs = append(m.inputs, ctx)

	outIndex, err := addStreamCopy(m.out, ctx, streamPos)
	if err != nil {
		return fmt.Errorf("add video stream: %w", err)
	}
	if displayAspect != "" {
		darNum, darDen, err := parseDisplayAspect(displayAspect)
		if err != nil {
			return err
		}
		C.reel_mux_set_display_aspect(m.out, outIndex, C.int(darNum), C.int(darDen))
	}

	feed, err := newMuxFeed(ctx, streamPos, outIndex, 0)
	if err != nil {
		return err
	}
	m.feeds = append(m.feeds, feed)
	return nil
}

func (m *nativeMuxer) addAudio(stream nativeaudio.EncodedStream) error {
	ctx, streamPos, err := openMuxInputBest(stream.Path, C.AVMEDIA_TYPE_AUDIO)
	if err != nil {
		return fmt.Errorf("open encoded audio %s: %w", stream.Path, err)
	}
	m.inputs = append(m.inputs, ctx)

	outIndex, err := addStreamCopy(m.out, ctx, streamPos)
	if err != nil {
		return fmt.Errorf("add audio stream %s: %w", stream.Path, err)
	}
	setAudioMetadata(m.out, outIndex, stream.Info)
	C.reel_mux_set_disposition(m.out, outIndex, C.int(audioDisposition(stream.Info.Disposition)))
	feed, err := newMuxFeed(ctx, streamPos, outIndex, offsetMicroseconds(stream.Info.StartOffsetSecs))
	if err != nil {
		return err
	}
	m.feeds = append(m.feeds, feed)
	return nil
}

func (m *nativeMuxer) copySourceMetadata(videoEndUS int64) error {
	if m.source == nil {
		return nil
	}
	C.reel_mux_copy_global_metadata(m.out, m.source)
	if ret := C.reel_mux_copy_chapters(m.out, m.source, C.int64_t(videoEndUS)); ret < 0 {
		return fmt.Errorf("copy chapters: %s", avError(ret))
	}
	return nil
}

func (m *nativeMuxer) write(outputPath string) error {
	cPath := C.CString(outputPath)
	defer C.free(unsafe.Pointer(cPath))

	m.out.flags |= C.AVFMT_FLAG_BITEXACT
	if C.reel_mux_needs_avio(m.out) != 0 {
		if ret := C.reel_mux_open_avio(m.out, cPath); ret < 0 {
			return fmt.Errorf("open output file: %s", avError(ret))
		}
		m.openedAVIO = true
	}
	if ret := C.reel_mux_write_header(m.out); ret < 0 {
		return fmt.Errorf("write container header: %s", avError(ret))
	}

	if err := m.muxPackets(); err != nil {
		return err
	}
	if ret := C.av_write_trailer(m.out); ret < 0 {
		return fmt.Errorf("write container trailer: %s", avError(ret))
	}
	return nil
}

func (m *nativeMuxer) muxPackets() error {
	for {
		for _, feed := range m.feeds {
			if !feed.hasPacket && !feed.done {
				if err := feed.fill(); err != nil {
					return err
				}
			}
		}

		var pick *muxFeed
		for _, feed := range m.feeds {
			if !feed.hasPacket {
				continue
			}
			if pick == nil || feed.timeUS < pick.timeUS {
				pick = feed
			}
		}
		if pick == nil {
			return nil
		}
		if err := pick.write(m.out); err != nil {
			return err
		}
	}
}

func (m *nativeMuxer) close() {
	for _, feed := range m.feeds {
		feed.close()
	}
	for _, ctx := range m.inputs {
		C.avformat_close_input(&ctx)
	}
	if m.source != nil {
		C.avformat_close_input(&m.source)
	}
	if m.openedAVIO {
		C.reel_mux_close_avio(m.out)
	}
	if m.out != nil {
		C.avformat_free_context(m.out)
	}
}

func newMuxFeed(ctx *C.AVFormatContext, streamPos C.int, outputIndex C.int, offsetUS int64) (*muxFeed, error) {
	packet := C.av_packet_alloc()
	if packet == nil {
		return nil, fmt.Errorf("allocate mux packet: out of memory")
	}
	stream := C.reel_mux_stream_at(ctx, streamPos)
	return &muxFeed{
		ctx:         ctx,
		packet:      packet,
		inputIndex:  C.reel_mux_stream_index(ctx, streamPos),
		outputIndex: outputIndex,
		timeBase:    stream.time_base,
		offsetUS:    offsetUS,
	}, nil
}

func (f *muxFeed) fill() error {
	for {
		ret := C.av_read_frame(f.ctx, f.packet)
		if ret < 0 {
			f.done = true
			return nil
		}
		if f.packet.stream_index != f.inputIndex {
			C.av_packet_unref(f.packet)
			continue
		}
		C.reel_mux_packet_shift_ts(f.packet, C.int64_t(f.offsetUS), f.timeBase)
		f.timeUS = int64(C.reel_mux_packet_time_us(f.packet, f.timeBase))
		f.hasPacket = true
		return nil
	}
}

func offsetMicroseconds(seconds float64) int64 {
	return int64(math.Round(seconds * 1_000_000))
}

func clampChapter(start, end, videoEnd int64) (int64, bool) {
	cEnd := C.int64_t(end)
	keep := C.reel_mux_clamp_chapter(C.int64_t(start), &cEnd, C.int64_t(videoEnd)) != 0
	return int64(cEnd), keep
}

func (f *muxFeed) write(out *C.AVFormatContext) error {
	outTimeBase := C.reel_mux_stream_time_base(out, f.outputIndex)
	C.reel_mux_packet_set_stream_index(f.packet, f.outputIndex)
	C.av_packet_rescale_ts(f.packet, f.timeBase, outTimeBase)
	ret := C.av_interleaved_write_frame(out, f.packet)
	C.av_packet_unref(f.packet)
	f.hasPacket = false
	if ret < 0 {
		return fmt.Errorf("write packet: %s", avError(ret))
	}
	return nil
}

func (f *muxFeed) close() {
	if f == nil || f.packet == nil {
		return
	}
	C.av_packet_free(&f.packet)
}

func openMuxInputBest(path string, mediaType C.enum_AVMediaType) (*C.AVFormatContext, C.int, error) {
	ctx, err := openMuxInput(path)
	if err != nil {
		return nil, -1, err
	}
	streamPos := C.reel_mux_best_stream(ctx, mediaType)
	if streamPos < 0 {
		C.avformat_close_input(&ctx)
		return nil, -1, fmt.Errorf("no %s stream found", mediaTypeName(mediaType))
	}
	return ctx, streamPos, nil
}

func openMuxInput(path string) (*C.AVFormatContext, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var ctx *C.AVFormatContext
	if ret := C.avformat_alloc_context(); ret == nil {
		return nil, fmt.Errorf("allocate input context: out of memory")
	} else {
		ctx = ret
	}
	C.reel_mux_set_probe_size(ctx)
	if ret := C.avformat_open_input(&ctx, cPath, nil, nil); ret < 0 {
		C.avformat_close_input(&ctx)
		return nil, fmt.Errorf("open input: %s", avError(ret))
	}
	if ret := C.avformat_find_stream_info(ctx, nil); ret < 0 {
		C.avformat_close_input(&ctx)
		return nil, fmt.Errorf("find stream info: %s", avError(ret))
	}
	return ctx, nil
}

func addStreamCopy(out, in *C.AVFormatContext, streamPos C.int) (C.int, error) {
	inStream := C.reel_mux_stream_at(in, streamPos)
	outIndex := C.reel_mux_add_stream_copy(out, inStream)
	if outIndex < 0 {
		return -1, fmt.Errorf("copy stream parameters: %s", avError(outIndex))
	}
	return outIndex, nil
}

func setAudioMetadata(ctx *C.AVFormatContext, outIndex C.int, stream media.AudioStreamInfo) {
	setStreamMetadata(ctx, outIndex, "language", stream.Language)
	setStreamMetadata(ctx, outIndex, "title", stream.Title)
}

func setStreamMetadata(ctx *C.AVFormatContext, streamIndex C.int, key, value string) {
	if value == "" {
		return
	}
	cKey := C.CString(key)
	defer C.free(unsafe.Pointer(cKey))
	cValue := C.CString(value)
	defer C.free(unsafe.Pointer(cValue))
	C.reel_mux_set_metadata(ctx, streamIndex, cKey, cValue)
}

func audioDisposition(d media.StreamDisposition) int {
	var flags int
	addFlag := func(enabled int, flag int) {
		if enabled != 0 {
			flags |= flag
		}
	}
	addFlag(d.Default, avDispositionDefault)
	addFlag(d.Dub, avDispositionDub)
	addFlag(d.Original, avDispositionOriginal)
	addFlag(d.Comment, avDispositionComment)
	addFlag(d.Lyrics, avDispositionLyrics)
	addFlag(d.Karaoke, avDispositionKaraoke)
	addFlag(d.Forced, avDispositionForced)
	addFlag(d.HearingImpaired, avDispositionHearingImpaired)
	addFlag(d.VisualImpaired, avDispositionVisualImpaired)
	addFlag(d.CleanEffects, avDispositionCleanEffects)
	addFlag(d.AttachedPic, avDispositionAttachedPic)
	addFlag(d.TimedThumbnails, avDispositionTimedThumbnails)
	return flags
}

func parseDisplayAspect(displayAspect string) (int, int, error) {
	parts := strings.Split(displayAspect, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid display aspect %q", displayAspect)
	}
	num, err := strconv.Atoi(parts[0])
	if err != nil || num <= 0 {
		return 0, 0, fmt.Errorf("invalid display aspect %q", displayAspect)
	}
	den, err := strconv.Atoi(parts[1])
	if err != nil || den <= 0 {
		return 0, 0, fmt.Errorf("invalid display aspect %q", displayAspect)
	}
	return num, den, nil
}

func mediaTypeName(mediaType C.enum_AVMediaType) string {
	switch mediaType {
	case C.AVMEDIA_TYPE_VIDEO:
		return "video"
	case C.AVMEDIA_TYPE_AUDIO:
		return "audio"
	default:
		return "media"
	}
}

func avError(ret C.int) string {
	msg := C.reel_mux_error_string(ret)
	if msg == nil {
		return fmt.Sprintf("error %d", int(ret))
	}
	defer C.av_free(unsafe.Pointer(msg))
	return C.GoString(msg)
}
