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
	r.Encoding = &EncodingReport{Snapshot: encodingstate.Snapshot{Resolution: "1280x720", DynamicRange: "HDR"}}
	got = computeSourceSummary(r)
	if got == nil || got.OutputResolution != "1280x720" || got.DynamicRange != "HDR" {
		t.Fatalf("snapshot: %+v", got)
	}
	if got := computeSourceSummary(&Report{}); got != nil {
		t.Fatalf("empty: %+v", got)
	}
}
