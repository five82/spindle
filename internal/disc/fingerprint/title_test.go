package fingerprint

import (
	"testing"

	"spindle/internal/disc"
)

func TestTitleHashDeterministic(t *testing.T) {
	title := disc.Title{
		ID:       5,
		Name:     "Episode One",
		Duration: 1800,
		Tracks: []disc.Track{
			{
				StreamID:     10,
				Order:        0,
				Type:         disc.TrackTypeVideo,
				CodecID:      "V_MPEG4/ISO/AVC",
				CodecShort:   "H.264",
				CodecLong:    "MPEG-4 AVC",
				Language:     "",
				ChannelCount: 0,
				Attributes:   map[int]string{9: "1920x1080"},
			},
			{
				StreamID:      20,
				Order:         1,
				Type:          disc.TrackTypeAudio,
				CodecID:       "A_AC3",
				CodecShort:    "AC3",
				CodecLong:     "Dolby Digital",
				Language:      "eng",
				LanguageName:  "English",
				ChannelCount:  6,
				ChannelLayout: "5.1 ch",
				BitRate:       "640000",
			},
		},
	}

	fp1 := TitleHash(title)
	if fp1 == "" {
		t.Fatal("expected hash to be generated")
	}

	// Reorder tracks; hash should remain stable.
	title.Tracks[0], title.Tracks[1] = title.Tracks[1], title.Tracks[0]
	fp2 := TitleHash(title)
	if fp1 != fp2 {
		t.Fatalf("expected deterministic hash, got %s vs %s", fp1, fp2)
	}
}

func TestTitleHashIgnoresDiscSpecificFields(t *testing.T) {
	base := disc.Title{
		Name:     "Episode Two",
		Duration: 1810,
		Tracks: []disc.Track{
			{StreamID: 1, Order: 0, Type: disc.TrackTypeVideo, CodecID: "V_H265", Attributes: map[int]string{1: "hdr10"}},
			{StreamID: 2, Order: 1, Type: disc.TrackTypeAudio, CodecID: "A_DTS", Language: "eng"},
		},
	}
	fp1 := TitleHash(base)

	modified := base
	modified.ID = 999
	fp2 := TitleHash(modified)

	if fp1 != fp2 {
		t.Fatalf("expected identical hash when disc-only fields differ, got %s vs %s", fp1, fp2)
	}
}
