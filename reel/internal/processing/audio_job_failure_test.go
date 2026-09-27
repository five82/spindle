package processing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/five82/reel/internal/media"
	"github.com/five82/reel/internal/perf"
	"github.com/five82/reel/internal/reporter"
)

func TestAudioJobFailureCancelsEncodeAndJoinIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	job := startAudioJob(ctx, cancel, "missing.wav", t.TempDir(), []media.AudioStreamInfo{{StreamIndex: 0, Channels: 1}}, 1, reporter.NullReporter{}, perf.New())
	_, err := job.join()
	if err == nil || !strings.Contains(err.Error(), "audio") || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("audio error: %v, context: %v", err, ctx.Err())
	}
	if _, again := job.join(); again != err {
		t.Fatalf("join lost error: %v / %v", err, again)
	}
}
