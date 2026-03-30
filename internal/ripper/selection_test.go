package ripper

import (
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestChoosePrimaryTitlePrefersMPLSWithSegments(t *testing.T) {
	titles := []ripspec.Title{
		{ID: 0, Duration: 10480, Chapters: 60, Playlist: "00070.m2ts", SegmentCount: 1},
		{ID: 2, Duration: 10480, Chapters: 60, Playlist: "00800.mpls", SegmentCount: 70},
		{ID: 1, Duration: 10480, Chapters: 60, Playlist: "00700.mpls", SegmentCount: 60},
	}
	selection, ok := ChoosePrimaryTitle(titles)
	if !ok {
		t.Fatalf("ChoosePrimaryTitle returned false")
	}
	if selection.ID != 2 {
		t.Fatalf("expected playlist with more segments (id=2), got id=%d", selection.ID)
	}
}

func TestChoosePrimaryTitleUsesHashWhenTied(t *testing.T) {
	titles := []ripspec.Title{
		{ID: 0, Duration: 5400, Chapters: 40, Playlist: "00100.mpls", SegmentCount: 10, TitleHash: "aaa"},
		{ID: 1, Duration: 5400, Chapters: 40, Playlist: "00101.mpls", SegmentCount: 10, TitleHash: "bbb"},
		{ID: 2, Duration: 5400, Chapters: 40, Playlist: "00102.mpls", SegmentCount: 10, TitleHash: "aaa"},
	}
	selection, ok := ChoosePrimaryTitle(titles)
	if !ok {
		t.Fatalf("ChoosePrimaryTitle returned false")
	}
	if selection.TitleHash != "aaa" {
		t.Fatalf("expected hash with highest frequency \"aaa\", got %q", selection.TitleHash)
	}
}

func TestChoosePrimaryTitleDisneyMultiLanguage(t *testing.T) {
	// Disney multi-language discs have 00800 (English), 00801 (French), 00802 (Spanish)
	// with nearly identical runtimes (differ by only a few seconds for localized credits).
	// Should prefer 00800.mpls (English).
	titles := []ripspec.Title{
		{ID: 0, Duration: 7200, Chapters: 20, Playlist: "00800.mpls", SegmentCount: 20},
		{ID: 1, Duration: 7205, Chapters: 20, Playlist: "00801.mpls", SegmentCount: 20}, // 5s longer (French credits)
		{ID: 2, Duration: 7203, Chapters: 20, Playlist: "00802.mpls", SegmentCount: 20}, // 3s longer (Spanish credits)
	}
	selection, ok := ChoosePrimaryTitle(titles)
	if !ok {
		t.Fatalf("ChoosePrimaryTitle returned false")
	}
	if selection.Playlist != "00800.mpls" {
		t.Fatalf("expected 00800.mpls (English), got %s", selection.Playlist)
	}
}

func TestChoosePrimaryTitleDifferentCutsNotDisneyPattern(t *testing.T) {
	// Discs with theatrical + director's cut have significantly different runtimes.
	// Should NOT apply Disney heuristic; should prefer the longer (director's cut).
	titles := []ripspec.Title{
		{ID: 0, Duration: 6783, Chapters: 17, Playlist: "00800.mpls", SegmentCount: 17}, // Theatrical: 113m 3s
		{ID: 1, Duration: 6991, Chapters: 17, Playlist: "00801.mpls", SegmentCount: 17}, // Director's: 116m 31s
	}
	selection, ok := ChoosePrimaryTitle(titles)
	if !ok {
		t.Fatalf("ChoosePrimaryTitle returned false")
	}
	if selection.ID != 1 {
		t.Fatalf("expected ID 1 (longer director's cut), got ID %d", selection.ID)
	}
	if selection.Duration != 6991 {
		t.Fatalf("expected duration 6991 (director's cut), got %d", selection.Duration)
	}
}

func TestChoosePrimaryTitleRuntimeThreshold(t *testing.T) {
	// At exactly 30 seconds difference, should still apply Disney heuristic.
	titles := []ripspec.Title{
		{ID: 0, Duration: 7200, Chapters: 20, Playlist: "00800.mpls", SegmentCount: 20},
		{ID: 1, Duration: 7230, Chapters: 20, Playlist: "00801.mpls", SegmentCount: 20}, // Exactly 30s longer
	}
	selection, ok := ChoosePrimaryTitle(titles)
	if !ok {
		t.Fatalf("ChoosePrimaryTitle returned false")
	}
	if selection.Playlist != "00800.mpls" {
		t.Fatalf("expected 00800.mpls at 30s threshold, got %s", selection.Playlist)
	}

	// At 31 seconds difference, should NOT apply Disney heuristic.
	titles = []ripspec.Title{
		{ID: 0, Duration: 7200, Chapters: 20, Playlist: "00800.mpls", SegmentCount: 20},
		{ID: 1, Duration: 7231, Chapters: 20, Playlist: "00801.mpls", SegmentCount: 20}, // 31s longer
	}
	selection, ok = ChoosePrimaryTitle(titles)
	if !ok {
		t.Fatalf("ChoosePrimaryTitle returned false")
	}
	if selection.ID != 1 {
		t.Fatalf("expected ID 1 (longer) at 31s, got ID %d", selection.ID)
	}
}
