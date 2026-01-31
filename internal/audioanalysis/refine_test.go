package audioanalysis

import (
	"testing"

	"spindle/internal/media/ffprobe"
)

func TestDeriveTempAudioPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/path/to/video.mkv", "/path/to/video.spindle-audio.mkv"},
		{"/path/to/video.mp4", "/path/to/video.spindle-audio.mp4"},
		{"/path/to/video", "/path/to/video.spindle-audio.mkv"},
		{"", ".spindle-audio"},
		{"  ", "  .spindle-audio"}, // whitespace-only path is not trimmed by deriveTempAudioPath
		{"/path/to/video.MKV", "/path/to/video.spindle-audio.MKV"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := deriveTempAudioPath(tt.input)
			if got != tt.want {
				t.Errorf("deriveTempAudioPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestOutputFormatForPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/video.mkv", "matroska"},
		{"/video.MKV", "matroska"},
		{"/video.mk3d", "matroska"},
		{"/video.mp4", "mp4"},
		{"/video.m4v", "mp4"},
		{"/video.mov", "mov"},
		{"/video.ts", "mpegts"},
		{"/video.m2ts", "mpegts"},
		{"/video.mka", "matroska"},
		{"/video.avi", ""},
		{"/video.unknown", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := outputFormatForPath(tt.path)
			if got != tt.want {
				t.Errorf("outputFormatForPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestNeedsDispositionFix(t *testing.T) {
	tests := []struct {
		name    string
		streams []ffprobe.Stream
		keep    []int
		want    bool
	}{
		{
			name:    "empty keep list",
			streams: []ffprobe.Stream{},
			keep:    []int{},
			want:    false,
		},
		{
			name: "primary already default",
			streams: []ffprobe.Stream{
				{Index: 1, CodecType: "audio", Disposition: map[string]int{"default": 1}},
				{Index: 2, CodecType: "audio", Disposition: map[string]int{}},
			},
			keep: []int{1, 2},
			want: false,
		},
		{
			name: "non-primary has default disposition",
			streams: []ffprobe.Stream{
				{Index: 1, CodecType: "audio", Disposition: map[string]int{}},
				{Index: 2, CodecType: "audio", Disposition: map[string]int{"default": 1}},
			},
			keep: []int{1, 2},
			want: true,
		},
		{
			name: "no dispositions set",
			streams: []ffprobe.Stream{
				{Index: 1, CodecType: "audio", Disposition: nil},
				{Index: 2, CodecType: "audio", Disposition: nil},
			},
			keep: []int{1, 2},
			want: false,
		},
		{
			name: "ignores video streams",
			streams: []ffprobe.Stream{
				{Index: 0, CodecType: "video", Disposition: map[string]int{"default": 1}},
				{Index: 1, CodecType: "audio", Disposition: map[string]int{}},
			},
			keep: []int{1},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := needsDispositionFix(tt.streams, tt.keep)
			if got != tt.want {
				t.Errorf("needsDispositionFix() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeCodecName(t *testing.T) {
	tests := []struct {
		codecName string
		profile   string
		want      string
	}{
		{"truehd", "", "TrueHD"},
		{"dts", "", "DTS"},
		{"dts", "DTS-HD MA", "DTS-HD MA"},
		{"dts", "DTS-HD", "DTS-HD"},
		{"eac3", "", "E-AC3"},
		{"ac3", "", "AC3"},
		{"flac", "", "FLAC"},
		{"pcm_s16le", "", "PCM"},
		{"pcm_s24le", "", "PCM"},
		{"pcm_s32le", "", "PCM"},
		{"aac", "", "AAC"},
		{"opus", "", "Opus"},
		{"vorbis", "", "VORBIS"},
	}

	for _, tt := range tests {
		t.Run(tt.codecName, func(t *testing.T) {
			stream := ffprobe.Stream{CodecName: tt.codecName, Profile: tt.profile}
			got := normalizeCodecName(stream)
			if got != tt.want {
				t.Errorf("normalizeCodecName(%q, %q) = %q, want %q", tt.codecName, tt.profile, got, tt.want)
			}
		})
	}
}

func TestFormatChannelLayout(t *testing.T) {
	tests := []struct {
		name     string
		layout   string
		channels int
		want     string
	}{
		{"mono from channels", "", 1, "mono"},
		{"stereo from channels", "", 2, "stereo"},
		{"5.1 from channels", "", 6, "5.1"},
		{"7.1 from channels", "", 8, "7.1"},
		{"4ch from channels", "", 4, "4ch"},
		{"unknown from 0 channels", "", 0, ""},
		{"5.1(side) normalized", "5.1(side)", 6, "5.1"},
		{"7.1(wide) normalized", "7.1(wide)", 8, "7.1"},
		{"explicit 5.1", "5.1", 6, "5.1"},
		{"explicit 7.1", "7.1", 8, "7.1"},
		{"passthrough unknown layout", "quad", 4, "quad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := ffprobe.Stream{ChannelLayout: tt.layout, Channels: tt.channels}
			got := formatChannelLayout(stream)
			if got != tt.want {
				t.Errorf("formatChannelLayout() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatAudioDescription(t *testing.T) {
	tests := []struct {
		name   string
		stream ffprobe.Stream
		want   string
	}{
		{
			name:   "non-audio stream",
			stream: ffprobe.Stream{CodecType: "video"},
			want:   "",
		},
		{
			name: "TrueHD 7.1 Atmos",
			stream: ffprobe.Stream{
				CodecType: "audio",
				CodecName: "truehd",
				Channels:  8,
				Profile:   "TrueHD Atmos",
			},
			want: "TrueHD 7.1 Atmos",
		},
		{
			name: "DTS-HD MA 5.1",
			stream: ffprobe.Stream{
				CodecType: "audio",
				CodecName: "dts",
				Channels:  6,
				Profile:   "DTS-HD MA",
			},
			want: "DTS-HD MA 5.1",
		},
		{
			name: "AAC stereo",
			stream: ffprobe.Stream{
				CodecType: "audio",
				CodecName: "aac",
				Channels:  2,
			},
			want: "AAC stereo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAudioDescription(tt.stream)
			if got != tt.want {
				t.Errorf("formatAudioDescription() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindAudioDescription(t *testing.T) {
	streams := []ffprobe.Stream{
		{Index: 0, CodecType: "video"},
		{Index: 1, CodecType: "audio", CodecName: "truehd", Channels: 8},
		{Index: 2, CodecType: "audio", CodecName: "ac3", Channels: 6},
	}

	tests := []struct {
		name  string
		index int
		want  string
	}{
		{"first audio when index negative", -1, "TrueHD 7.1"},
		{"specific index 1", 1, "TrueHD 7.1"},
		{"specific index 2", 2, "AC3 5.1"},
		{"nonexistent index", 99, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findAudioDescription(streams, tt.index)
			if got != tt.want {
				t.Errorf("findAudioDescription(streams, %d) = %q, want %q", tt.index, got, tt.want)
			}
		})
	}
}

func TestSummarizeAudioCandidates(t *testing.T) {
	streams := []ffprobe.Stream{
		{Index: 0, CodecType: "video"},
		{
			Index:     1,
			CodecType: "audio",
			CodecLong: "TrueHD",
			Channels:  8,
			Tags:      map[string]string{"language": "eng"},
		},
		{
			Index:     2,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  6,
			Tags:      map[string]string{"language": "fra"},
		},
	}

	candidates, hasEnglish := summarizeAudioCandidates(streams)

	if !hasEnglish {
		t.Error("expected hasEnglish=true")
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if candidates[0].Index != 1 {
		t.Errorf("first candidate index = %d, want 1", candidates[0].Index)
	}
	if candidates[1].Index != 2 {
		t.Errorf("second candidate index = %d, want 2", candidates[1].Index)
	}
}

func TestSummarizeAudioCandidatesNoEnglish(t *testing.T) {
	streams := []ffprobe.Stream{
		{
			Index:     1,
			CodecType: "audio",
			CodecName: "ac3",
			Channels:  6,
			Tags:      map[string]string{"language": "fra"},
		},
	}

	_, hasEnglish := summarizeAudioCandidates(streams)

	if hasEnglish {
		t.Error("expected hasEnglish=false for French-only streams")
	}
}
