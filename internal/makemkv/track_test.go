package makemkv

import "testing"

func TestClassifyTrackType(t *testing.T) {
	tests := []struct {
		input string
		want  TrackType
	}{
		{"Video", TrackTypeVideo},
		{"video", TrackTypeVideo},
		{"MPEG-4 AVC Video", TrackTypeVideo},
		{"Audio", TrackTypeAudio},
		{"Dolby TrueHD Audio", TrackTypeAudio},
		{"Subtitle", TrackTypeSubtitle},
		{"PGS Subtitles", TrackTypeSubtitle},
		{"Closed Captions (text)", TrackTypeSubtitle},
		{"Data", TrackTypeData},
		{"", TrackTypeUnknown},
		{"Other", TrackTypeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := classifyTrackType(tt.input)
			if got != tt.want {
				t.Errorf("classifyTrackType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTrackIsAudio(t *testing.T) {
	if !(Track{Type: TrackTypeAudio}).IsAudio() {
		t.Error("audio track: IsAudio() = false")
	}
	if (Track{Type: TrackTypeVideo}).IsAudio() {
		t.Error("video track: IsAudio() = true")
	}
}

func TestTrackIsForced(t *testing.T) {
	tests := []struct {
		name string
		tr   Track
		want bool
	}{
		{"forced subtitle", Track{Type: TrackTypeSubtitle, Name: "PGS English (forced only)"}, true},
		{"non-forced subtitle", Track{Type: TrackTypeSubtitle, Name: "PGS English"}, false},
		{"forced audio (not subtitle)", Track{Type: TrackTypeAudio, Name: "Audio (forced only)"}, false},
		{"forced case insensitive", Track{Type: TrackTypeSubtitle, Name: "PGS English (Forced Only)"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tr.IsForced(); got != tt.want {
				t.Errorf("IsForced() = %v, want %v", got, tt.want)
			}
		})
	}
}
