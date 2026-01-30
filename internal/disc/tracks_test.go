package disc

import "testing"

func TestTrackIsForced(t *testing.T) {
	tests := []struct {
		name     string
		track    Track
		expected bool
	}{
		{
			name: "forced english subtitle",
			track: Track{
				Type:     TrackTypeSubtitle,
				Name:     "PGS English  (forced only)",
				Language: "eng",
			},
			expected: true,
		},
		{
			name: "regular english subtitle",
			track: Track{
				Type:     TrackTypeSubtitle,
				Name:     "PGS English",
				Language: "eng",
			},
			expected: false,
		},
		{
			name: "forced subtitle lowercase",
			track: Track{
				Type:     TrackTypeSubtitle,
				Name:     "pgs english (Forced Only)",
				Language: "eng",
			},
			expected: true,
		},
		{
			name: "forced subtitle mixed case",
			track: Track{
				Type:     TrackTypeSubtitle,
				Name:     "Subtitles (FORCED ONLY)",
				Language: "eng",
			},
			expected: true,
		},
		{
			name: "audio track with forced in name",
			track: Track{
				Type:     TrackTypeAudio,
				Name:     "Audio (forced only)",
				Language: "eng",
			},
			expected: false,
		},
		{
			name: "video track",
			track: Track{
				Type: TrackTypeVideo,
				Name: "Video",
			},
			expected: false,
		},
		{
			name: "subtitle without forced marker",
			track: Track{
				Type:     TrackTypeSubtitle,
				Name:     "Spanish subtitles",
				Language: "spa",
			},
			expected: false,
		},
		{
			name: "forced in middle of name",
			track: Track{
				Type:     TrackTypeSubtitle,
				Name:     "English forced subtitles",
				Language: "eng",
			},
			expected: false,
		},
		{
			name: "empty name subtitle",
			track: Track{
				Type:     TrackTypeSubtitle,
				Name:     "",
				Language: "eng",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.track.IsForced()
			if result != tt.expected {
				t.Errorf("IsForced() = %v, want %v", result, tt.expected)
			}
		})
	}
}
