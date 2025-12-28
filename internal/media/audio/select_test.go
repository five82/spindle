package audio

import (
	"testing"

	"spindle/internal/media/ffprobe"
)

func TestSelectPrefersHighestChannelCount(t *testing.T) {
	streams := []ffprobe.Stream{
		{Index: 0, CodecType: "video"},
		{
			Index:       1,
			CodecType:   "audio",
			CodecName:   "truehd",
			CodecLong:   "Dolby TrueHD with Atmos",
			Channels:    8,
			Tags:        map[string]string{"language": "eng", "title": "Dolby TrueHD Atmos 7.1"},
			Disposition: map[string]int{"default": 1},
		},
		{
			Index:     2,
			CodecType: "audio",
			CodecName: "dts",
			CodecLong: "DTS-HD Master Audio",
			Channels:  6,
			Tags:      map[string]string{"language": "eng", "title": "DTS-HD MA 5.1"},
		},
		{
			Index:     3,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Stereo"},
		},
	}

	sel := Select(streams)
	if sel.PrimaryIndex != 1 {
		t.Fatalf("expected 8-channel lossless track (index 1) to be selected, got %d", sel.PrimaryIndex)
	}
	if len(sel.KeepIndices) != 1 || sel.KeepIndices[0] != 1 {
		t.Fatalf("expected only primary to be kept, got %v", sel.KeepIndices)
	}
	if !sel.Changed(3) {
		t.Fatal("expected selection to remove at least one audio track")
	}
}

func TestSelectPrefersLosslessOverLossy(t *testing.T) {
	streams := []ffprobe.Stream{
		{
			Index:     1,
			CodecType: "audio",
			CodecName: "dts",
			CodecLong: "DTS-HD Master Audio",
			Channels:  6,
			Tags:      map[string]string{"language": "eng"},
		},
		{
			Index:     2,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  6,
			Tags:      map[string]string{"language": "eng"},
		},
	}

	sel := Select(streams)
	if sel.PrimaryIndex != 1 {
		t.Fatalf("expected lossless DTS-HD MA (index 1) over lossy AC3, got %d", sel.PrimaryIndex)
	}
	if len(sel.KeepIndices) != 1 || sel.KeepIndices[0] != 1 {
		t.Fatalf("expected only primary to be kept, got %v", sel.KeepIndices)
	}
}

func TestSelectFallsBackWhenNoEnglish(t *testing.T) {
	streams := []ffprobe.Stream{
		{
			Index:     0,
			CodecType: "audio",
			CodecName: "dts",
			Channels:  6,
			Tags:      map[string]string{"language": "jpn"},
		},
		{
			Index:     1,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "fra"},
		},
	}

	sel := Select(streams)
	if sel.PrimaryIndex != 0 {
		t.Fatalf("expected first audio stream to be primary fallback, got %d", sel.PrimaryIndex)
	}
	if len(sel.KeepIndices) != 1 || sel.KeepIndices[0] != 0 {
		t.Fatalf("expected only primary to be kept, got %v", sel.KeepIndices)
	}
}
