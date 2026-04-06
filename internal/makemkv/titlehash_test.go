package makemkv

import "testing"

func TestTitleHashDeterministic(t *testing.T) {
	title := TitleInfo{
		ID:       0,
		Name:     "Main Feature",
		Duration: 5400,
		Tracks: []Track{
			{StreamID: 0, Order: 0, Type: TrackTypeVideo, CodecID: "V_MPEG4"},
			{StreamID: 1, Order: 1, Type: TrackTypeAudio, CodecID: "A_DTS", Language: "eng", ChannelCount: 6},
		},
	}

	hash1 := TitleHash(title)
	hash2 := TitleHash(title)

	if hash1 != hash2 {
		t.Errorf("non-deterministic: %q != %q", hash1, hash2)
	}
	if len(hash1) != 64 {
		t.Errorf("expected 64-char hex string, got len %d", len(hash1))
	}
}

func TestTitleHashDifferentTracks(t *testing.T) {
	base := TitleInfo{Name: "Title", Duration: 3600}

	withDTS := base
	withDTS.Tracks = []Track{{StreamID: 0, Type: TrackTypeAudio, CodecID: "A_DTS"}}

	withAC3 := base
	withAC3.Tracks = []Track{{StreamID: 0, Type: TrackTypeAudio, CodecID: "A_AC3"}}

	if TitleHash(withDTS) == TitleHash(withAC3) {
		t.Error("different codecs should produce different hashes")
	}
}

func TestTitleHashEmpty(t *testing.T) {
	hash1 := TitleHash(TitleInfo{})
	hash2 := TitleHash(TitleInfo{})

	if hash1 != hash2 {
		t.Error("empty titles should produce identical hashes")
	}
	if hash1 == "" {
		t.Error("hash should not be empty")
	}
}

func TestTitleHashTrackOrderIndependent(t *testing.T) {
	// Tracks with different insertion order but same StreamIDs should
	// produce the same hash (sorted by StreamID before hashing).
	tracks := []Track{
		{StreamID: 2, Order: 0, Type: TrackTypeSubtitle, Language: "eng"},
		{StreamID: 0, Order: 1, Type: TrackTypeVideo, CodecID: "V_H265"},
		{StreamID: 1, Order: 2, Type: TrackTypeAudio, CodecID: "A_DTS"},
	}
	reversed := []Track{
		{StreamID: 0, Order: 1, Type: TrackTypeVideo, CodecID: "V_H265"},
		{StreamID: 1, Order: 2, Type: TrackTypeAudio, CodecID: "A_DTS"},
		{StreamID: 2, Order: 0, Type: TrackTypeSubtitle, Language: "eng"},
	}

	t1 := TitleInfo{Name: "Test", Duration: 100, Tracks: tracks}
	t2 := TitleInfo{Name: "Test", Duration: 100, Tracks: reversed}

	if TitleHash(t1) != TitleHash(t2) {
		t.Error("track input order should not affect hash")
	}
}
