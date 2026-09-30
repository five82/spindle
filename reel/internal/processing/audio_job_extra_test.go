package processing

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/reporter"
	"github.com/five82/spindle/reel/internal/video"
)

func TestEmptyAudioJobAndDisplaySummary(t *testing.T) {
	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	job := startAudioJob(ctx, cancel, "missing", t.TempDir(), nil, 1, reporter.NullReporter{}, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})), nil)
	first, err := job.join()
	if err != nil || first != nil {
		t.Fatalf("audio = %v, %v", first, err)
	}
	second, err := job.join()
	if err != nil || second != nil || logs.Len() != 0 {
		t.Fatalf("repeated join = %v, %v, logs=%s", second, err, logs.String())
	}
	cfg := &config.Config{}
	if got := cvvdpDisplaySummary(cfg, nil); !strings.Contains(got, "generated SDR") {
		t.Fatal(got)
	}
	pq := int32(16)
	if got := cvvdpDisplaySummary(cfg, &video.Info{TransferCharacteristics: &pq}); !strings.Contains(got, "generated HDR") {
		t.Fatal(got)
	}
	cfg.CVVDPDisplay = "custom.json"
	if got := cvvdpDisplaySummary(cfg, nil); !strings.Contains(got, "override=custom.json") {
		t.Fatal(got)
	}
}
