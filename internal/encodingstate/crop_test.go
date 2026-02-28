package encodingstate

import "testing"

func TestParseCropFilter(t *testing.T) {
	tests := []struct {
		filter string
		wantW  int
		wantH  int
		wantOK bool
	}{
		{"crop=1920:800:0:140", 1920, 800, true},
		{"1920:800:0:140", 1920, 800, true},
		{"crop=3840:1600:0:280", 3840, 1600, true},
		{"", 0, 0, false},
		{"invalid", 0, 0, false},
		{"crop=abc:def:0:0", 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			w, h, ok := ParseCropFilter(tt.filter)
			if ok != tt.wantOK {
				t.Fatalf("ParseCropFilter(%q) ok=%v, want %v", tt.filter, ok, tt.wantOK)
			}
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("ParseCropFilter(%q) = (%d, %d), want (%d, %d)", tt.filter, w, h, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestMatchStandardRatio(t *testing.T) {
	tests := []struct {
		ratio float64
		want  string
	}{
		{1.33, "4:3"},
		{1.78, "16:9"},
		{1.85, "1.85:1"},
		{2.00, "2.00:1"},
		{2.20, "2.20:1"},
		{2.35, "2.35:1"},
		{2.39, "2.39:1"},
		{2.40, "2.40:1"},
		{3.00, "3.00:1"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := MatchStandardRatio(tt.ratio)
			if got != tt.want {
				t.Errorf("MatchStandardRatio(%.2f) = %q, want %q", tt.ratio, got, tt.want)
			}
		})
	}
}
