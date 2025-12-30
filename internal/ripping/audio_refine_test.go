package ripping

import (
	"testing"

	"spindle/internal/media/ffprobe"
)

func TestCommentaryLabel(t *testing.T) {
	tests := []struct {
		name     string
		original string
		want     string
	}{
		{name: "empty", original: "", want: "Commentary"},
		{name: "already commentary", original: "Director Commentary", want: "Director Commentary"},
		{name: "append commentary", original: "Stereo", want: "Stereo (Commentary)"},
	}
	for _, test := range tests {
		if got := commentaryLabel(test.original); got != test.want {
			t.Fatalf("%s: expected %q, got %q", test.name, test.want, got)
		}
	}
}

func TestNeedsCommentaryDispositionFix(t *testing.T) {
	streams := []ffprobe.Stream{
		{
			Index:       0,
			CodecType:   "audio",
			Tags:        map[string]string{"title": "English"},
			Disposition: map[string]int{"default": 1},
		},
		{
			Index:       1,
			CodecType:   "audio",
			Tags:        map[string]string{"title": "Director Commentary"},
			Disposition: map[string]int{"comment": 1},
		},
	}
	commentaryTitles := map[int]string{1: "Director Commentary"}
	if needsCommentaryDispositionFix(streams, commentaryTitles) {
		t.Fatalf("expected no commentary disposition fix when comment flag and title present")
	}

	streams[1].Disposition = map[string]int{"default": 0}
	if !needsCommentaryDispositionFix(streams, commentaryTitles) {
		t.Fatalf("expected commentary disposition fix when comment flag missing")
	}

	streams[1].Disposition = map[string]int{"comment": 1}
	commentaryTitles[1] = ""
	if !needsCommentaryDispositionFix(streams, commentaryTitles) {
		t.Fatalf("expected commentary disposition fix when title missing")
	}
}
