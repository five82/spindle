package audio

import (
	"testing"

	"spindle/internal/media/ffprobe"
)

func TestSelectPrefersSpatialAudio(t *testing.T) {
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
			Tags:      map[string]string{"language": "eng", "title": "Director Commentary"},
		},
	}

	sel := Select(streams)
	if sel.PrimaryIndex != 1 {
		t.Fatalf("expected primary index 1, got %d", sel.PrimaryIndex)
	}
	if len(sel.CommentaryIndices) != 1 || sel.CommentaryIndices[0] != 3 {
		t.Fatalf("expected commentary track 3, got %v", sel.CommentaryIndices)
	}
	if !sel.Changed(3) {
		t.Fatal("expected selection to remove at least one audio track")
	}
}

func TestSelectFallsBackToLosslessWhenNoSpatial(t *testing.T) {
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
		t.Fatalf("expected DTS-HD MA (index 1) to be selected, got %d", sel.PrimaryIndex)
	}
	if len(sel.CommentaryIndices) != 0 {
		t.Fatalf("expected no commentary tracks, got %v", sel.CommentaryIndices)
	}
}

func TestSelectTreatsStereoAsCommentaryWhenMultichannelExists(t *testing.T) {
	streams := []ffprobe.Stream{
		{
			Index:     0,
			CodecType: "audio",
			CodecName: "dts",
			CodecLong: "DTS-HD Master Audio",
			Channels:  6,
			Tags:      map[string]string{"language": "eng", "title": "Main Feature"},
		},
		{
			Index:     1,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Stereo Mix"},
		},
	}

	sel := Select(streams)
	if sel.PrimaryIndex != 0 {
		t.Fatalf("expected index 0 to be primary, got %d", sel.PrimaryIndex)
	}
	if len(sel.CommentaryIndices) != 1 || sel.CommentaryIndices[0] != 1 {
		t.Fatalf("expected stereo track as commentary, got %v", sel.CommentaryIndices)
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
	if len(sel.CommentaryIndices) != 0 {
		t.Fatalf("expected no commentary for non-English disc, got %v", sel.CommentaryIndices)
	}
}
