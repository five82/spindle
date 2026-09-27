package chunk

import (
	"testing"

	"github.com/five82/reel/internal/media"
)

func TestAudioDispositionBitmask(t *testing.T) {
	disposition := media.StreamDisposition{Default: 1, Forced: 1, HearingImpaired: 1}

	got := audioDisposition(disposition)
	want := avDispositionDefault | avDispositionForced | avDispositionHearingImpaired
	if got != want {
		t.Fatalf("audioDisposition() = %d, want %d", got, want)
	}
}

func TestClampChapterToVideoEnd(t *testing.T) {
	end, keep := clampChapter(55, 95, 60)
	if !keep || end != 60 {
		t.Fatalf("clampChapter() = (%d, %t), want (60, true)", end, keep)
	}

	_, keep = clampChapter(60, 95, 60)
	if keep {
		t.Fatal("clampChapter() retained a chapter beginning at the video end")
	}
}

func TestOffsetMicroseconds(t *testing.T) {
	tests := []struct {
		seconds float64
		want    int64
	}{
		{seconds: 0.501, want: 501000},
		{seconds: -0.0065, want: -6500},
		{seconds: 0.0000005, want: 1},
	}
	for _, tt := range tests {
		if got := offsetMicroseconds(tt.seconds); got != tt.want {
			t.Errorf("offsetMicroseconds(%g) = %d, want %d", tt.seconds, got, tt.want)
		}
	}
}

func TestParseDisplayAspect(t *testing.T) {
	num, den, err := parseDisplayAspect("16:9")
	if err != nil {
		t.Fatalf("parseDisplayAspect() error = %v", err)
	}
	if num != 16 || den != 9 {
		t.Fatalf("parseDisplayAspect() = %d:%d, want 16:9", num, den)
	}
}
