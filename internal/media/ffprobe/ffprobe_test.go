package ffprobe

import (
	"encoding/json"
	"testing"
)

func TestVideoStreamCount(t *testing.T) {
	r := &Result{
		Streams: []Stream{
			{Index: 0, CodecType: "video"},
			{Index: 1, CodecType: "audio"},
			{Index: 2, CodecType: "video"},
			{Index: 3, CodecType: "subtitle"},
		},
	}
	if got := r.VideoStreamCount(); got != 2 {
		t.Errorf("VideoStreamCount() = %d, want 2", got)
	}
}

func TestAudioStreamCount(t *testing.T) {
	r := &Result{
		Streams: []Stream{
			{Index: 0, CodecType: "video"},
			{Index: 1, CodecType: "audio"},
			{Index: 2, CodecType: "audio"},
			{Index: 3, CodecType: "audio"},
		},
	}
	if got := r.AudioStreamCount(); got != 3 {
		t.Errorf("AudioStreamCount() = %d, want 3", got)
	}
}

func TestDurationSeconds(t *testing.T) {
	tests := []struct {
		name     string
		duration string
		want     float64
	}{
		{"valid", "7200.123456", 7200.123456},
		{"empty", "", 0},
		{"invalid", "not_a_number", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Result{Format: Format{Duration: tt.duration}}
			if got := r.DurationSeconds(); got != tt.want {
				t.Errorf("DurationSeconds() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSizeBytes(t *testing.T) {
	tests := []struct {
		name string
		size string
		want int64
	}{
		{"valid", "1073741824", 1073741824},
		{"empty", "", 0},
		{"invalid", "abc", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Result{Format: Format{Size: tt.size}}
			if got := r.SizeBytes(); got != tt.want {
				t.Errorf("SizeBytes() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBitRate(t *testing.T) {
	tests := []struct {
		name    string
		bitRate string
		want    int64
	}{
		{"valid", "5000000", 5000000},
		{"empty", "", 0},
		{"invalid", "high", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Result{Format: Format{BitRate: tt.bitRate}}
			if got := r.BitRate(); got != tt.want {
				t.Errorf("BitRate() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRawJSON(t *testing.T) {
	raw := []byte(`{"streams":[],"format":{}}`)
	r := &Result{rawJSON: raw}
	got := r.RawJSON()
	if string(got) != string(raw) {
		t.Errorf("RawJSON() = %s, want %s", got, raw)
	}
}

func TestEmptyStreams(t *testing.T) {
	r := &Result{}
	if got := r.VideoStreamCount(); got != 0 {
		t.Errorf("VideoStreamCount() on empty = %d, want 0", got)
	}
	if got := r.AudioStreamCount(); got != 0 {
		t.Errorf("AudioStreamCount() on empty = %d, want 0", got)
	}
}

func TestJSONParsing(t *testing.T) {
	sample := `{
		"streams": [
			{
				"index": 0,
				"codec_name": "hevc",
				"codec_type": "video",
				"codec_tag_string": "hvc1",
				"codec_long_name": "H.265 / HEVC",
				"width": 3840,
				"height": 2160,
				"profile": "Main 10",
				"tags": {"language": "eng"},
				"disposition": {"default": 1, "forced": 0}
			},
			{
				"index": 1,
				"codec_name": "truehd",
				"codec_type": "audio",
				"codec_tag_string": "[0][0][0][0]",
				"codec_long_name": "TrueHD",
				"sample_rate": "48000",
				"channels": 8,
				"channel_layout": "7.1",
				"bit_rate": "4640000",
				"tags": {"language": "eng"},
				"disposition": {"default": 1}
			},
			{
				"index": 2,
				"codec_name": "hdmv_pgs_subtitle",
				"codec_type": "subtitle",
				"tags": {"language": "eng"},
				"disposition": {"default": 0, "forced": 0}
			}
		],
		"format": {
			"filename": "/media/disc.mkv",
			"nb_streams": 3,
			"duration": "7200.500000",
			"size": "42949672960",
			"bit_rate": "47721858",
			"format_name": "matroska,webm"
		}
	}`

	var result Result
	if err := json.Unmarshal([]byte(sample), &result); err != nil {
		t.Fatalf("failed to parse sample JSON: %v", err)
	}
	result.rawJSON = []byte(sample)

	if got := len(result.Streams); got != 3 {
		t.Fatalf("stream count = %d, want 3", got)
	}
	if got := result.VideoStreamCount(); got != 1 {
		t.Errorf("VideoStreamCount() = %d, want 1", got)
	}
	if got := result.AudioStreamCount(); got != 1 {
		t.Errorf("AudioStreamCount() = %d, want 1", got)
	}

	if result.Streams[0].Width != 3840 || result.Streams[0].Height != 2160 {
		t.Errorf("video dimensions = %dx%d, want 3840x2160",
			result.Streams[0].Width, result.Streams[0].Height)
	}
	if result.Streams[1].Channels != 8 {
		t.Errorf("audio channels = %d, want 8", result.Streams[1].Channels)
	}
	if result.Streams[1].ChannelLayout != "7.1" {
		t.Errorf("channel layout = %q, want %q", result.Streams[1].ChannelLayout, "7.1")
	}

	if got := result.DurationSeconds(); got != 7200.5 {
		t.Errorf("DurationSeconds() = %v, want 7200.5", got)
	}
	if got := result.SizeBytes(); got != 42949672960 {
		t.Errorf("SizeBytes() = %d, want 42949672960", got)
	}
	if got := result.BitRate(); got != 47721858 {
		t.Errorf("BitRate() = %d, want 47721858", got)
	}

	if result.Format.Filename != "/media/disc.mkv" {
		t.Errorf("filename = %q, want /media/disc.mkv", result.Format.Filename)
	}
	if result.Format.FormatName != "matroska,webm" {
		t.Errorf("format_name = %q, want matroska,webm", result.Format.FormatName)
	}
	if result.Format.NBStreams != 3 {
		t.Errorf("nb_streams = %d, want 3", result.Format.NBStreams)
	}

	if lang, ok := result.Streams[0].Tags["language"]; !ok || lang != "eng" {
		t.Errorf("stream 0 language tag = %q, want eng", lang)
	}
	if def, ok := result.Streams[0].Disposition["default"]; !ok || def != 1 {
		t.Errorf("stream 0 default disposition = %d, want 1", def)
	}

	if string(result.RawJSON()) != sample {
		t.Error("RawJSON() does not match original input")
	}
}
