package subtitle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
)

func TestSourceProfileFallsBackToDiscAndResolution(t *testing.T) {
	original := inspectSubtitleMedia
	t.Cleanup(func() { inspectSubtitleMedia = original })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	inspectSubtitleMedia = func(context.Context, string, string) (*ffprobe.Result, error) {
		return nil, errors.New("probe unavailable")
	}
	if got := subtitleSourceProfile(context.Background(), logger, "missing.mkv", "DVD"); got.class != "dvd" || got.resolution() != "unknown" {
		t.Fatalf("DVD fallback: %+v", got)
	}
	if got := subtitleSourceProfile(context.Background(), logger, "missing.mkv", "bluray"); got.class != "bluray" {
		t.Fatalf("Blu-ray fallback: %+v", got)
	}
	if got := subtitleSourceProfile(context.Background(), logger, "missing.mkv", "laserdisc"); got.class != "unknown" {
		t.Fatalf("unknown format: %+v", got)
	}
	for _, tc := range []struct {
		height int
		class  string
	}{{480, "dvd"}, {1080, "bluray"}, {2160, "uhd"}} {
		inspectSubtitleMedia = func(context.Context, string, string) (*ffprobe.Result, error) {
			return &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "audio", Width: 5000, Height: 5000}, {CodecType: "video", Width: tc.height * 16 / 9, Height: tc.height}}}, nil
		}
		got := subtitleSourceProfile(context.Background(), logger, "movie.mkv", "")
		if got.class != tc.class || got.height != tc.height {
			t.Fatalf("height %d: %+v", tc.height, got)
		}
	}
}
