package identification

import (
	"testing"

	"spindle/internal/disc"
)

func TestDetermineBestTitlePrefersMakeMKV(t *testing.T) {
	scan := sampleScanResult("Good Title", "BD Name")
	got := determineBestTitle("Current", scan)
	if got != "Good Title" {
		t.Fatalf("expected MakeMKV title, got %q", got)
	}
}

func TestDetermineBestTitleFallsBackToBDInfo(t *testing.T) {
	scan := sampleScanResult("DVD_VIDEO", "BD Name")
	got := determineBestTitle("Current", scan)
	if got != "BD Name" {
		t.Fatalf("expected BDInfo title, got %q", got)
	}
}

func TestDetermineBestTitleFallsBackToCurrent(t *testing.T) {
	scan := sampleScanResult("DVD_VIDEO", "UNKNOWN DISC")
	got := determineBestTitle("My Movie", scan)
	if got != "My Movie" {
		t.Fatalf("expected current title, got %q", got)
	}
}

func TestIsPlaceholderTitle(t *testing.T) {
	cases := []struct {
		title     string
		discLabel string
		want      bool
	}{
		{title: "", discLabel: "", want: true},
		{title: "Unknown Disc", discLabel: "", want: true},
		{title: "Unknown Disc 2", discLabel: "", want: true},
		{title: "DISC_ONE", discLabel: "disc_one", want: true},
		{title: "My Movie", discLabel: "", want: false},
		{title: "Some Label", discLabel: "Other", want: false},
	}
	for _, tc := range cases {
		if got := isPlaceholderTitle(tc.title, tc.discLabel); got != tc.want {
			t.Fatalf("isPlaceholderTitle(%q,%q)=%v want %v", tc.title, tc.discLabel, got, tc.want)
		}
	}
}

func sampleScanResult(makemkvTitle, bdTitle string) *disc.ScanResult {
	scan := &disc.ScanResult{}
	if makemkvTitle != "" {
		scan.Titles = []disc.Title{{ID: 1, Name: makemkvTitle}}
	}
	if bdTitle != "" {
		scan.BDInfo = &disc.BDInfoResult{DiscName: bdTitle}
	}
	return scan
}
