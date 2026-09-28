package ui

import (
	"testing"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func TestSourceSummaryVariations(t *testing.T) {
	for _, tc := range []struct {
		src  *spindle.SourceTitle
		want string
	}{
		{nil, ""},
		{&spindle.SourceTitle{}, "Title 00"},
		{&spindle.SourceTitle{TitleID: 2}, "Title 02"},
		{&spindle.SourceTitle{Name: " Main Feature ", TitleID: 2, DurationSeconds: 7200}, "Main Feature (120m)"},
	} {
		if got := sourceSummary(tc.src); got != tc.want {
			t.Fatalf("sourceSummary(%+v) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestThemeCycleOrderAndUnknown(t *testing.T) {
	for _, tc := range []struct{ current, want string }{{"Slate", "Nightfox"}, {"Nightfox", "Slate"}, {"unknown", "Slate"}} {
		if got := NextTheme(tc.current); got != tc.want {
			t.Fatalf("NextTheme(%q) = %q, want %q", tc.current, got, tc.want)
		}
	}
}
