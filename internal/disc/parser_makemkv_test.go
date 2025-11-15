package disc

import "testing"

func TestMakeMKVParserExtractsTracks(t *testing.T) {
	input := `
TINFO:0,2,0,"Main Feature"
TINFO:0,8,0,"24"
TINFO:0,9,0,"1:39:03"
TINFO:0,16,0,"00800.mpls"
TINFO:0,25,0,"1"
TINFO:0,26,0,"1,2,3"
SINFO:0,0,1,4352,"Video"
SINFO:0,0,6,4352,"MPEG-4 AVC"
SINFO:0,1,1,4353,"Audio"
SINFO:0,1,3,4353,"eng"
SINFO:0,1,4,4353,"English"
SINFO:0,1,6,4353,"TrueHD"
SINFO:0,1,7,4353,"Dolby TrueHD with Atmos"
SINFO:0,1,14,4353,"8"
SINFO:0,1,30,4353,"Main Audio"
SINFO:0,2,1,4354,"Audio"
SINFO:0,2,3,4354,"eng"
SINFO:0,2,4,4354,"English"
SINFO:0,2,6,4354,"AC3"
SINFO:0,2,7,4354,"Dolby Digital"
SINFO:0,2,14,4354,"2"
SINFO:0,2,30,4354,"Director Commentary"
`

	parser := makeMKVParser{}
	result, err := parser.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(result.Titles) != 1 {
		t.Fatalf("expected 1 title, got %d", len(result.Titles))
	}
	title := result.Titles[0]
	if title.Name != "Main Feature" {
		t.Fatalf("unexpected title name: %q", title.Name)
	}
	if title.Duration != 5943 {
		t.Fatalf("unexpected duration: %d", title.Duration)
	}
	if title.Chapters != 24 {
		t.Fatalf("unexpected chapter count: %d", title.Chapters)
	}
	if title.Playlist != "00800.mpls" {
		t.Fatalf("unexpected playlist: %q", title.Playlist)
	}
	if title.SegmentCount != 3 {
		t.Fatalf("unexpected segment count: %d", title.SegmentCount)
	}
	if len(title.Tracks) != 3 {
		t.Fatalf("expected 3 tracks, got %d", len(title.Tracks))
	}

	var mainAudio, commentary Track
	for _, track := range title.Tracks {
		if track.Type == TrackTypeAudio && track.ChannelCount == 8 {
			mainAudio = track
		}
		if track.Type == TrackTypeAudio && track.ChannelCount == 2 {
			commentary = track
		}
	}

	if mainAudio.StreamID == 0 {
		t.Fatal("expected to find primary audio track")
	}
	if mainAudio.CodecLong != "Dolby TrueHD with Atmos" {
		t.Fatalf("unexpected codec long name: %q", mainAudio.CodecLong)
	}
	if commentary.Name != "Director Commentary" {
		t.Fatalf("unexpected commentary title: %q", commentary.Name)
	}
}
