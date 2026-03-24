package audio

import (
	"reflect"
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
)

// mkStream is a test helper that builds an audio stream with common defaults.
func mkStream(index int, codec, lang string, channels int, opts ...func(*ffprobe.Stream)) ffprobe.Stream {
	s := ffprobe.Stream{
		Index:     index,
		CodecName: codec,
		CodecType: "audio",
		Channels:  channels,
		Tags:      map[string]string{"language": lang},
		Disposition: map[string]int{},
	}
	for _, opt := range opts {
		opt(&s)
	}
	return s
}

func withDefault() func(*ffprobe.Stream) {
	return func(s *ffprobe.Stream) {
		s.Disposition["default"] = 1
	}
}

func withTitle(title string) func(*ffprobe.Stream) {
	return func(s *ffprobe.Stream) {
		s.Tags["title"] = title
	}
}

func withCodecLong(name string) func(*ffprobe.Stream) {
	return func(s *ffprobe.Stream) {
		s.CodecLong = name
	}
}

func withProfile(profile string) func(*ffprobe.Stream) {
	return func(s *ffprobe.Stream) {
		s.Profile = profile
	}
}

func withLayout(layout string) func(*ffprobe.Stream) {
	return func(s *ffprobe.Stream) {
		s.ChannelLayout = layout
		s.Channels = 0 // force layout parsing
	}
}

func TestSelect(t *testing.T) {
	tests := []struct {
		name           string
		streams        []ffprobe.Stream
		wantIndex      int
		wantKeep       []int
		wantRemoved    []int
	}{
		{
			name: "single English stream selected",
			streams: []ffprobe.Stream{
				mkStream(0, "aac", "eng", 2, withDefault()),
			},
			wantIndex:   0,
			wantKeep:    []int{0},
			wantRemoved: nil,
		},
		{
			name: "multiple English streams highest channel count wins",
			streams: []ffprobe.Stream{
				mkStream(0, "aac", "eng", 2),
				mkStream(1, "ac3", "eng", 6),
				mkStream(2, "truehd", "eng", 8),
			},
			wantIndex:   2,
			wantKeep:    []int{2},
			wantRemoved: []int{0, 1},
		},
		{
			name: "lossless bonus in scoring",
			streams: []ffprobe.Stream{
				mkStream(0, "ac3", "eng", 6),
				mkStream(1, "truehd", "eng", 6),
			},
			wantIndex:   1,
			wantKeep:    []int{1},
			wantRemoved: []int{0},
		},
		{
			name: "no English streams fallback to first",
			streams: []ffprobe.Stream{
				mkStream(0, "aac", "spa", 6),
				mkStream(1, "ac3", "fra", 6),
			},
			wantIndex:   0,
			wantKeep:    []int{0},
			wantRemoved: []int{1},
		},
		{
			name: "default flag tiebreaker",
			streams: []ffprobe.Stream{
				mkStream(0, "aac", "eng", 6),
				mkStream(1, "aac", "eng", 6, withDefault()),
			},
			wantIndex:   1,
			wantKeep:    []int{1},
			wantRemoved: []int{0},
		},
		{
			name:        "empty streams",
			streams:     nil,
			wantIndex:   0,
			wantKeep:    nil,
			wantRemoved: nil,
		},
		{
			name: "non-audio streams ignored",
			streams: []ffprobe.Stream{
				{Index: 0, CodecType: "video", CodecName: "h264"},
				mkStream(1, "aac", "eng", 2),
				{Index: 2, CodecType: "subtitle", CodecName: "subrip"},
			},
			wantIndex:   1,
			wantKeep:    []int{1},
			wantRemoved: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel := Select(tt.streams, nil)

			if len(tt.streams) == 0 {
				if sel.KeepIndices != nil {
					t.Errorf("expected nil KeepIndices for empty input, got %v", sel.KeepIndices)
				}
				return
			}

			if sel.PrimaryIndex != tt.wantIndex {
				t.Errorf("PrimaryIndex = %d, want %d", sel.PrimaryIndex, tt.wantIndex)
			}
			if !reflect.DeepEqual(sel.KeepIndices, tt.wantKeep) {
				t.Errorf("KeepIndices = %v, want %v", sel.KeepIndices, tt.wantKeep)
			}
			if !reflect.DeepEqual(sel.RemovedIndices, tt.wantRemoved) {
				t.Errorf("RemovedIndices = %v, want %v", sel.RemovedIndices, tt.wantRemoved)
			}
		})
	}
}

func TestIsSpatialAudio(t *testing.T) {
	tests := []struct {
		name   string
		stream ffprobe.Stream
		want   bool
	}{
		{
			name:   "atmos in title",
			stream: mkStream(0, "truehd", "eng", 8, withTitle("TrueHD Atmos 7.1")),
			want:   true,
		},
		{
			name:   "atmos in codec long name",
			stream: mkStream(0, "truehd", "eng", 8, withCodecLong("TrueHD Atmos")),
			want:   true,
		},
		{
			name: "dts:x in profile",
			stream: mkStream(0, "dts", "eng", 8, withProfile("DTS:X")),
			want: true,
		},
		{
			name:   "imax enhanced in title",
			stream: mkStream(0, "dts", "eng", 6, withTitle("DTS IMAX Enhanced")),
			want:   true,
		},
		{
			name:   "not spatial",
			stream: mkStream(0, "ac3", "eng", 6),
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSpatialAudio(tt.stream)
			if got != tt.want {
				t.Errorf("isSpatialAudio() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsLosslessCodec(t *testing.T) {
	tests := []struct {
		name   string
		stream ffprobe.Stream
		want   bool
	}{
		{
			name:   "truehd",
			stream: mkStream(0, "truehd", "eng", 8),
			want:   true,
		},
		{
			name:   "flac",
			stream: mkStream(0, "flac", "eng", 2),
			want:   true,
		},
		{
			name:   "pcm_s24le",
			stream: mkStream(0, "pcm_s24le", "eng", 2),
			want:   true,
		},
		{
			name:   "lossless in long name",
			stream: mkStream(0, "custom", "eng", 2, withCodecLong("Some Lossless Codec")),
			want:   true,
		},
		{
			name:   "master audio in long name",
			stream: mkStream(0, "dts", "eng", 6, withCodecLong("DTS-HD Master Audio")),
			want:   true,
		},
		{
			name:   "dts-hd in long name",
			stream: mkStream(0, "dts", "eng", 6, withCodecLong("DTS-HD")),
			want:   true,
		},
		{
			name:   "lossy codec",
			stream: mkStream(0, "aac", "eng", 2),
			want:   false,
		},
		{
			name:   "ac3 lossy",
			stream: mkStream(0, "ac3", "eng", 6),
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLosslessCodec(tt.stream)
			if got != tt.want {
				t.Errorf("isLosslessCodec() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseChannelCount(t *testing.T) {
	tests := []struct {
		name    string
		stream  ffprobe.Stream
		want    int
	}{
		{
			name:   "channels field preferred",
			stream: mkStream(0, "aac", "eng", 6),
			want:   6,
		},
		{
			name:   "7.1 layout",
			stream: mkStream(0, "aac", "eng", 0, withLayout("7.1")),
			want:   8,
		},
		{
			name:   "5.1(side) layout",
			stream: mkStream(0, "aac", "eng", 0, withLayout("5.1(side)")),
			want:   6,
		},
		{
			name:   "stereo layout",
			stream: mkStream(0, "aac", "eng", 0, withLayout("stereo")),
			want:   2,
		},
		{
			name:   "mono layout",
			stream: mkStream(0, "aac", "eng", 0, withLayout("mono")),
			want:   1,
		},
		{
			name:   "empty layout zero channels",
			stream: mkStream(0, "aac", "eng", 0, withLayout("")),
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseChannelCount(tt.stream)
			if got != tt.want {
				t.Errorf("parseChannelCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPrimaryLabel(t *testing.T) {
	tests := []struct {
		name string
		sel  Selection
		want string
	}{
		{
			name: "with title",
			sel: Selection{
				Primary: mkStream(0, "truehd", "eng", 8, withTitle("TrueHD Atmos 7.1")),
			},
			want: "English | truehd | 8ch | TrueHD Atmos 7.1",
		},
		{
			name: "without title",
			sel: Selection{
				Primary: mkStream(0, "aac", "eng", 2),
			},
			want: "English | aac | 2ch",
		},
		{
			name: "non-english language",
			sel: Selection{
				Primary: mkStream(0, "ac3", "spa", 6),
			},
			want: "Spanish | ac3 | 6ch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.sel.PrimaryLabel()
			if got != tt.want {
				t.Errorf("PrimaryLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChanged(t *testing.T) {
	tests := []struct {
		name       string
		sel        Selection
		totalAudio int
		want       bool
	}{
		{
			name: "no change single stream",
			sel: Selection{
				KeepIndices:    []int{0},
				RemovedIndices: nil,
			},
			totalAudio: 1,
			want:       false,
		},
		{
			name: "streams removed",
			sel: Selection{
				KeepIndices:    []int{0},
				RemovedIndices: []int{1, 2},
			},
			totalAudio: 3,
			want:       true,
		},
		{
			name: "total mismatch",
			sel: Selection{
				KeepIndices:    []int{0},
				RemovedIndices: nil,
			},
			totalAudio: 3,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.sel.Changed(tt.totalAudio)
			if got != tt.want {
				t.Errorf("Changed(%d) = %v, want %v", tt.totalAudio, got, tt.want)
			}
		})
	}
}
