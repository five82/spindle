package queue

import "testing"

func TestDisplayTitleFallsBackThroughMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, disc, metadata, want string
		id                         int64
	}{
		{"disc", " Disc Label ", `{"display_title":"Movie"}`, "Disc Label", 1},
		{"display year", "", `{"display_title":"Movie","year":"2024"}`, "Movie (2024)", 1},
		{"display already dated", "", `{"display_title":"Movie (2024)","year":"2024"}`, "Movie (2024)", 1},
		{"show season", "", `{"show_title":"Show","season_number":2}`, "Show Season 02", 1},
		{"show", "", `{"show_title":"Show"}`, "Show", 1},
		{"title year", "", `{"title":"Film","year":"2020"}`, "Film (2020)", 1},
		{"title", "", `{"title":"Film"}`, "Film", 1},
		{"id", "", "", "Item 12", 12},
		{"empty", "", "", "Unknown Item", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := &Item{ID: tc.id, DiscTitle: tc.disc, MetadataJSON: tc.metadata}
			if got := item.DisplayTitle(); got != tc.want {
				t.Fatalf("DisplayTitle = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPrimaryReviewReason(t *testing.T) {
	item := &Item{}
	if got := item.PrimaryReviewReason(); got != "" {
		t.Fatalf("empty reason: %q", got)
	}
	item.AppendReviewReason("first")
	item.AppendReviewReason("second")
	if got := item.PrimaryReviewReason(); got != "first" {
		t.Fatalf("primary reason: %q", got)
	}
}
