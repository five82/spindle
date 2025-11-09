package ripping

import "testing"

func TestParseTitleID(t *testing.T) {
	tests := []struct {
		name string
		want int
		ok   bool
	}{
		{"South Park Season 5 - Disc 1_t00.mkv", 0, true},
		{"South Park Season 5 - Disc 1_t07.mkv", 7, true},
		{"title_t12.mkv", 12, true},
		{"TITLE_T42.MKV", 42, true},
		{"bonus-feature.mkv", 0, false},
	}

	for _, tt := range tests {
		got, ok := parseTitleID(tt.name)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Fatalf("parseTitleID(%q) = (%d,%v); want (%d,%v)", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}
