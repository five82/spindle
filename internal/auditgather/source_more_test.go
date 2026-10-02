package auditgather

import (
	"testing"

	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/ffprobe"
)

func TestSourceSummaryInfersVideoFromProbeAndFileDecision(t *testing.T) {
	r := &Report{Item: ItemSummary{DiscTitle: "Example UHD"}, Logs: &LogAnalysis{Decisions: []LogDecision{{DecisionType: logs.DecisionFileProbe, Extras: map[string]any{"resolution": "3840x2160", "codecs": "hevc,aac"}}}}, Media: []MediaFileProbe{{Probe: &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "audio", CodecName: "aac"}, {CodecType: "video", CodecName: "hevc", Width: 3840, Height: 2160, ColorTransfer: "smpte2084"}}}}}}
	got := computeSourceSummary(r)
	if got == nil || got.InputResolution != "3840x2160" || got.OutputResolution != "3840x2160" || got.OutputCodec != "hevc" || !got.HDR || !got.UHDLikely || len(got.InputCodecs) != 2 {
		t.Fatalf("source: %+v", got)
	}
	r.Logs.Decisions[0].Extras = nil
	r.Logs.Decisions[0].DecisionReason = "resolution=1920x1080 codecs=h264,aac"
	got = computeSourceSummary(r)
	if got == nil || got.InputResolution != "1920x1080" || len(got.InputCodecs) != 2 || got.InputCodecs[0] != "h264" {
		t.Fatalf("fallback: %+v", got)
	}
	// A cropped encode: the snapshot's Resolution is the input, never the output,
	// and the delivered probe outranks the (last-episode) snapshot.
	r.Media[0].Probe.Streams[1].Width, r.Media[0].Probe.Streams[1].Height = 1440, 1080
	r.Encoding = &EncodingReport{Snapshot: encodingstate.Snapshot{Resolution: "1920x1080", OutputResolution: "1436x1080", DynamicRange: "HDR"}}
	got = computeSourceSummary(r)
	if got == nil || got.OutputResolution != "1440x1080" || got.DynamicRange != "HDR" {
		t.Fatalf("cropped probe: %+v", got)
	}
	r.Media = nil
	got = computeSourceSummary(r)
	if got == nil || got.OutputResolution != "1436x1080" {
		t.Fatalf("snapshot output fallback: %+v", got)
	}
	if got := computeSourceSummary(&Report{}); got != nil {
		t.Fatalf("empty: %+v", got)
	}
}
