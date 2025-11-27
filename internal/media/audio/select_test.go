package audio

import (
	"testing"

	"spindle/internal/media/ffprobe"
)

func TestSelectPrefersHighestChannelCount(t *testing.T) {
	// Spatial audio (Atmos/DTS:X) metadata is stripped during Opus transcoding,
	// so we prioritize channel count + lossless quality instead.
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
		t.Fatalf("expected 8-channel lossless track (index 1) to be selected, got %d", sel.PrimaryIndex)
	}
	if len(sel.CommentaryIndices) != 1 || sel.CommentaryIndices[0] != 3 {
		t.Fatalf("expected commentary track 3, got %v", sel.CommentaryIndices)
	}
	if !sel.Changed(3) {
		t.Fatal("expected selection to remove at least one audio track")
	}
}

func TestSelectPrefersLosslessOverLossy(t *testing.T) {
	// When channel count is equal, prefer lossless (better source for Opus transcode)
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
	if len(sel.CommentaryIndices) != 0 {
		t.Fatalf("expected no commentary tracks, got %v", sel.CommentaryIndices)
	}
}

func TestSelectDoesNotTreatStereoAsCommentaryWithoutExplicitMarkers(t *testing.T) {
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
	// Stereo mix should NOT be treated as commentary without explicit markers
	if len(sel.CommentaryIndices) != 0 {
		t.Fatalf("expected no commentary tracks without explicit markers, got %v", sel.CommentaryIndices)
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

func TestSelectDoesNotTreatDirectorApprovedMixAsCommentary(t *testing.T) {
	streams := []ffprobe.Stream{
		{
			Index:     0,
			CodecType: "audio",
			CodecName: "dts",
			Channels:  6,
			Tags:      map[string]string{"language": "eng", "title": "Primary"},
		},
		{
			Index:     1,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Director Approved Mix"},
		},
	}

	sel := Select(streams)
	if len(sel.CommentaryIndices) != 0 {
		t.Fatalf("expected no commentary classification, got %v", sel.CommentaryIndices)
	}
}

func TestSelectDetectsCommentaryFromCommentTag(t *testing.T) {
	streams := []ffprobe.Stream{
		{
			Index:     0,
			CodecType: "audio",
			CodecName: "dts",
			Channels:  6,
			Tags:      map[string]string{"language": "eng", "title": "Main"},
		},
		{
			Index:     1,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags: map[string]string{
				"language": "eng",
				"title":    "Untitled",
				"comment":  "Audio Commentary with Director",
			},
		},
	}

	sel := Select(streams)
	if len(sel.CommentaryIndices) != 1 || sel.CommentaryIndices[0] != 1 {
		t.Fatalf("expected commentary track at index 1, got %v", sel.CommentaryIndices)
	}
}

func TestSelectSkipsDubDispositionForCommentary(t *testing.T) {
	streams := []ffprobe.Stream{
		{
			Index:     0,
			CodecType: "audio",
			CodecName: "dts",
			Channels:  6,
			Tags:      map[string]string{"language": "eng", "title": "Main"},
		},
		{
			Index:       1,
			CodecType:   "audio",
			CodecName:   "ac3",
			Channels:    2,
			Tags:        map[string]string{"language": "eng", "title": "Dub", "comment": "Audio Commentary"},
			Disposition: map[string]int{"dub": 1},
		},
	}

	sel := Select(streams)
	if len(sel.CommentaryIndices) != 0 {
		t.Fatalf("expected dub track to be ignored for commentary, got %v", sel.CommentaryIndices)
	}
}

func TestSelectDetectsDiscussionKeywords(t *testing.T) {
	streams := []ffprobe.Stream{
		{
			Index:     0,
			CodecType: "audio",
			CodecName: "truehd",
			Channels:  8,
			Tags:      map[string]string{"language": "eng", "title": "Dolby TrueHD"},
		},
		{
			Index:     1,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags: map[string]string{
				"language": "eng",
				"title":    "Director and Writer Discussion",
			},
		},
	}

	sel := Select(streams)
	if len(sel.CommentaryIndices) != 1 || sel.CommentaryIndices[0] != 1 {
		t.Fatalf("expected discussion track classified as commentary, got %v", sel.CommentaryIndices)
	}
}

func TestSelectDetectsExplicitCommentaryDisposition(t *testing.T) {
	streams := []ffprobe.Stream{
		{
			Index:     0,
			CodecType: "audio",
			CodecName: "dts",
			Channels:  6,
			Tags:      map[string]string{"language": "eng", "title": "Main"},
		},
		{
			Index:       1,
			CodecType:   "audio",
			CodecName:   "ac3",
			Channels:    2,
			Tags:        map[string]string{"language": "eng", "title": "Track 2"},
			Disposition: map[string]int{"commentary": 1},
		},
	}

	sel := Select(streams)
	if sel.PrimaryIndex != 0 {
		t.Fatalf("expected index 0 to be primary, got %d", sel.PrimaryIndex)
	}
	if len(sel.CommentaryIndices) != 1 || sel.CommentaryIndices[0] != 1 {
		t.Fatalf("expected track with commentary disposition to be included, got %v", sel.CommentaryIndices)
	}
}

func TestSelectOnlyRipsPrimaryWhenNoCommentaryDetected(t *testing.T) {
	streams := []ffprobe.Stream{
		{
			Index:     0,
			CodecType: "audio",
			CodecName: "truehd",
			Channels:  8,
			Tags:      map[string]string{"language": "eng", "title": "Dolby TrueHD Atmos"},
		},
		{
			Index:     1,
			CodecType: "audio",
			CodecName: "dts",
			Channels:  6,
			Tags:      map[string]string{"language": "eng", "title": "DTS-HD MA 5.1"},
		},
		{
			Index:     2,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Stereo"},
		},
		{
			Index:     3,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "fra", "title": "French 2.0"},
		},
	}

	sel := Select(streams)
	if sel.PrimaryIndex != 0 {
		t.Fatalf("expected Atmos track (index 0) to be primary, got %d", sel.PrimaryIndex)
	}
	// Should only keep primary track when no commentary is explicitly detected
	if len(sel.CommentaryIndices) != 0 {
		t.Fatalf("expected no commentary tracks, got %v", sel.CommentaryIndices)
	}
	if len(sel.KeepIndices) != 1 {
		t.Fatalf("expected only 1 track to be kept (primary), got %v", sel.KeepIndices)
	}
	if len(sel.RemovedIndices) != 3 {
		t.Fatalf("expected 3 tracks to be removed, got %v", sel.RemovedIndices)
	}
}

func TestSelectDetectsCommentaryFromSequentialStereoPattern(t *testing.T) {
	// South Park pattern: multichannel tracks followed by 2 identical stereo tracks
	// (first stereo = downmix, second stereo = commentary)
	streams := []ffprobe.Stream{
		{Index: 0, CodecType: "video"},
		{
			Index:       1,
			CodecType:   "audio",
			CodecName:   "truehd",
			Channels:    6,
			Tags:        map[string]string{"language": "eng", "title": "Surround 5.1"},
			Disposition: map[string]int{"default": 1},
		},
		{
			Index:     2,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  6,
			Tags:      map[string]string{"language": "eng", "title": "Surround 5.1"},
		},
		{
			Index:     3,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Stereo"},
		},
		{
			Index:     4,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Stereo"},
		},
	}

	sel := Select(streams)
	if sel.PrimaryIndex != 1 {
		t.Fatalf("expected TrueHD 5.1 (index 1) to be primary, got %d", sel.PrimaryIndex)
	}
	// Heuristic should detect second stereo track as commentary
	if len(sel.CommentaryIndices) != 1 || sel.CommentaryIndices[0] != 4 {
		t.Fatalf("expected commentary track at index 4, got %v", sel.CommentaryIndices)
	}
	// Should keep primary + commentary (downmix at index 3 is redundant)
	if len(sel.KeepIndices) != 2 {
		t.Fatalf("expected 2 tracks to be kept, got %v", sel.KeepIndices)
	}
	expectedKeep := []int{1, 4}
	for i, idx := range expectedKeep {
		if sel.KeepIndices[i] != idx {
			t.Fatalf("expected KeepIndices %v, got %v", expectedKeep, sel.KeepIndices)
		}
	}
}

func TestSelectDetectsMultipleCommentariesFromSequentialStereoPattern(t *testing.T) {
	// Movie pattern: multichannel + 3 identical sequential stereo tracks
	// (first stereo = downmix, rest = multiple commentaries)
	streams := []ffprobe.Stream{
		{Index: 0, CodecType: "video"},
		{
			Index:       1,
			CodecType:   "audio",
			CodecName:   "truehd",
			Channels:    8,
			Tags:        map[string]string{"language": "eng", "title": "Atmos 7.1"},
			Disposition: map[string]int{"default": 1},
		},
		{
			Index:     2,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Stereo"},
		},
		{
			Index:     3,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Stereo"},
		},
		{
			Index:     4,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Stereo"},
		},
	}

	sel := Select(streams)
	if sel.PrimaryIndex != 1 {
		t.Fatalf("expected Atmos (index 1) to be primary, got %d", sel.PrimaryIndex)
	}
	// Heuristic should detect second and third stereo tracks as commentary
	if len(sel.CommentaryIndices) != 2 {
		t.Fatalf("expected 2 commentary tracks, got %v", sel.CommentaryIndices)
	}
	if sel.CommentaryIndices[0] != 3 || sel.CommentaryIndices[1] != 4 {
		t.Fatalf("expected commentary tracks at indices 3 and 4, got %v", sel.CommentaryIndices)
	}
}

func TestSelectDoesNotApplyHeuristicWithNonSequentialStereo(t *testing.T) {
	// Pattern with gap in stereo tracks should NOT trigger heuristic
	streams := []ffprobe.Stream{
		{Index: 0, CodecType: "video"},
		{
			Index:     1,
			CodecType: "audio",
			CodecName: "truehd",
			Channels:  6,
			Tags:      map[string]string{"language": "eng", "title": "Surround 5.1"},
		},
		{
			Index:     2,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Stereo"},
		},
		{
			Index:     3,
			CodecType: "audio",
			CodecName: "dts",
			Channels:  6,
			Tags:      map[string]string{"language": "fra", "title": "French 5.1"},
		},
		{
			Index:     4,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Stereo"},
		},
	}

	sel := Select(streams)
	// Should not detect commentary due to non-sequential stereo tracks
	if len(sel.CommentaryIndices) != 0 {
		t.Fatalf("expected no commentary tracks with non-sequential stereo, got %v", sel.CommentaryIndices)
	}
}

func TestSelectHeuristicDoesNotOverrideExplicitCommentary(t *testing.T) {
	// When explicit commentary flag exists, don't apply heuristic
	streams := []ffprobe.Stream{
		{Index: 0, CodecType: "video"},
		{
			Index:     1,
			CodecType: "audio",
			CodecName: "truehd",
			Channels:  6,
			Tags:      map[string]string{"language": "eng", "title": "Surround 5.1"},
		},
		{
			Index:     2,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Stereo"},
		},
		{
			Index:     3,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  2,
			Tags:      map[string]string{"language": "eng", "title": "Stereo"},
		},
		{
			Index:       4,
			CodecType:   "audio",
			CodecName:   "ac3",
			Channels:    2,
			Tags:        map[string]string{"language": "eng", "title": "Director Commentary"},
			Disposition: map[string]int{"commentary": 1},
		},
	}

	sel := Select(streams)
	// Should only keep the explicit commentary (index 4), not apply heuristic to indices 2-3
	if len(sel.CommentaryIndices) != 1 || sel.CommentaryIndices[0] != 4 {
		t.Fatalf("expected only explicit commentary at index 4, got %v", sel.CommentaryIndices)
	}
}
