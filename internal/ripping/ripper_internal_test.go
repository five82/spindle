package ripping

import (
	"testing"

	"spindle/internal/ripspec"
)

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

func TestChoosePrimaryTitlePrefersMPLSWithSegments(t *testing.T) {
	titles := []ripspec.Title{
		{ID: 0, Duration: 10480, Chapters: 60, Playlist: "00070.m2ts", SegmentCount: 1},
		{ID: 2, Duration: 10480, Chapters: 60, Playlist: "00800.mpls", SegmentCount: 70},
		{ID: 1, Duration: 10480, Chapters: 60, Playlist: "00700.mpls", SegmentCount: 60},
	}
	selection, ok := ChoosePrimaryTitle(titles)
	if !ok {
		t.Fatalf("choosePrimaryTitle returned false")
	}
	if selection.ID != 2 {
		t.Fatalf("expected playlist with more segments (id=2), got id=%d", selection.ID)
	}
}

func TestChoosePrimaryTitleUsesFingerprintWhenTied(t *testing.T) {
	titles := []ripspec.Title{
		{ID: 0, Duration: 5400, Chapters: 40, Playlist: "00100.mpls", SegmentCount: 10, ContentFingerprint: "aaa"},
		{ID: 1, Duration: 5400, Chapters: 40, Playlist: "00101.mpls", SegmentCount: 10, ContentFingerprint: "bbb"},
		{ID: 2, Duration: 5400, Chapters: 40, Playlist: "00102.mpls", SegmentCount: 10, ContentFingerprint: "aaa"},
	}
	selection, ok := ChoosePrimaryTitle(titles)
	if !ok {
		t.Fatalf("choosePrimaryTitle returned false")
	}
	if selection.ContentFingerprint != "aaa" {
		t.Fatalf("expected fingerprint with highest frequency \"aaa\", got %q", selection.ContentFingerprint)
	}
}
