package encodingstate

import (
	"testing"
)

func TestIsZero(t *testing.T) {
	tests := []struct {
		name string
		snap Snapshot
		want bool
	}{
		{
			name: "empty snapshot is zero",
			snap: Snapshot{},
			want: true,
		},
		{
			name: "snapshot with percent is not zero",
			snap: Snapshot{Percent: 50.0},
			want: false,
		},
		{
			name: "snapshot with substage is not zero",
			snap: Snapshot{Substage: "encoding"},
			want: false,
		},
		{
			name: "snapshot with error is not zero",
			snap: Snapshot{Error: &Issue{Title: "fail"}},
			want: false,
		},
		{
			name: "snapshot with crop_required true is not zero",
			snap: Snapshot{CropRequired: true},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.snap.IsZero(); got != tt.want {
				t.Errorf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReset(t *testing.T) {
	s := Snapshot{
		Percent:    75.5,
		Substage:   "encoding",
		InputFile:  "/tmp/movie.mkv",
		FPS:        24.0,
		Error:      &Issue{Title: "oops"},
		Validation: &Validation{Passed: true},
	}
	s.Reset()
	if !s.IsZero() {
		t.Errorf("Reset() did not produce zero snapshot: %+v", s)
	}
}

func TestMarshalZero(t *testing.T) {
	s := Snapshot{}
	if got := s.Marshal(); got != "" {
		t.Errorf("Marshal() of zero snapshot = %q, want empty string", got)
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	original := Snapshot{
		Percent:               42.5,
		ETASeconds:            120.0,
		FPS:                   30.0,
		CurrentFrame:          1000,
		TotalFrames:           5000,
		CurrentOutputBytes:    1024,
		EstimatedTotalBytes:   5120,
		Substage:              "encoding",
		InputFile:             "/tmp/video.mkv",
		Resolution:            "1920x1080",
		DynamicRange:          "SDR",
		Encoder:               "svt-av1",
		Preset:                "6",
		Quality:               "30",
		Tune:                  "0",
		AudioCodec:            "opus",
		DraptoPreset:          "film",
		CropFilter:            "crop=1920:800:0:140",
		CropRequired:          true,
		CropMessage:           "letterbox detected",
		OriginalSize:          5000000,
		EncodedSize:           2000000,
		SizeReductionPercent:  60.0,
		AverageSpeed:          1.5,
		EncodeDurationSeconds: 300.0,
		Warning:               "slow encode",
		Error:                 &Issue{Title: "warning", Message: "minor issue"},
		Validation:            &Validation{Passed: true, Steps: []ValidationStep{{Name: "size", Passed: true, Details: "ok"}}},
	}

	raw := original.Marshal()
	if raw == "" {
		t.Fatal("Marshal() returned empty string for non-zero snapshot")
	}

	restored, err := Unmarshal(raw)
	if err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	// Compare marshalled output to verify round-trip fidelity.
	if got := restored.Marshal(); got != raw {
		t.Errorf("round-trip mismatch:\n  got:  %s\n  want: %s", got, raw)
	}
}

func TestUnmarshalEmpty(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty string", input: ""},
		{name: "whitespace only", input: "   \t\n  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Unmarshal(tt.input)
			if err != nil {
				t.Fatalf("Unmarshal(%q) error: %v", tt.input, err)
			}
			if !s.IsZero() {
				t.Errorf("Unmarshal(%q) returned non-zero snapshot", tt.input)
			}
		})
	}
}

func TestUnmarshalInvalid(t *testing.T) {
	_, err := Unmarshal("not json")
	if err == nil {
		t.Error("Unmarshal(invalid) should return error")
	}
}

func TestParseCropFilter(t *testing.T) {
	tests := []struct {
		name       string
		filter     string
		wantW      int
		wantH      int
		wantErr    bool
	}{
		{
			name:   "crop= prefix format",
			filter: "crop=1920:800:0:140",
			wantW:  1920,
			wantH:  800,
		},
		{
			name:   "bare format",
			filter: "1920:1080:0:0",
			wantW:  1920,
			wantH:  1080,
		},
		{
			name:    "empty string",
			filter:  "",
			wantErr: true,
		},
		{
			name:    "missing fields",
			filter:  "1920:800",
			wantErr: true,
		},
		{
			name:    "non-numeric",
			filter:  "crop=abc:def:0:0",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, h, err := ParseCropFilter(tt.filter)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("got %dx%d, want %dx%d", w, h, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestMatchStandardRatio(t *testing.T) {
	tests := []struct {
		name  string
		ratio float64
		want  string
	}{
		{name: "4:3 exact", ratio: 1.333, want: "1.33:1"},
		{name: "16:9 exact", ratio: 1.778, want: "1.78:1"},
		{name: "1.85:1 exact", ratio: 1.850, want: "1.85:1"},
		{name: "2.00:1 exact", ratio: 2.000, want: "2.00:1"},
		{name: "2.20:1 exact", ratio: 2.200, want: "2.20:1"},
		{name: "2.35:1 exact", ratio: 2.350, want: "2.35:1"},
		{name: "2.39:1 exact", ratio: 2.390, want: "2.39:1"},
		{name: "2.40:1 exact", ratio: 2.400, want: "2.40:1"},
		{name: "16:9 within tolerance", ratio: 1.790, want: "1.78:1"},
		{name: "2.39:1 within tolerance", ratio: 2.380, want: "2.39:1"},
		{name: "no match", ratio: 3.000, want: "3.00:1"},
		{name: "no match unusual", ratio: 1.600, want: "1.60:1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchStandardRatio(tt.ratio); got != tt.want {
				t.Errorf("MatchStandardRatio(%v) = %q, want %q", tt.ratio, got, tt.want)
			}
		})
	}
}
