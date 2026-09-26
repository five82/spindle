package subtitle

import (
	"context"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/ripspec"
)

func TestRunSkipsUnconfiguredAndCompletedSubtitles(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{{Key: "s01e01"}, {Key: "s01e02"}},
		Assets: ripspec.Assets{
			Ripped: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/tmp/one.mkv", Status: ripspec.AssetStatusCompleted},
				{EpisodeKey: "s01e02", Path: "/tmp/two.mkv", Status: ripspec.AssetStatusCompleted},
			},
			Subtitled: []ripspec.Asset{{EpisodeKey: "s01e02", Path: "/tmp/two.srt", Status: ripspec.AssetStatusCompleted}},
		},
	}
	sess := newSubtitleTestSession(t, env)
	h := New(&config.Config{}, nil, nil)
	if err := h.Run(context.Background(), sess); err != nil {
		t.Fatalf("disabled: %v", err)
	}
	h.cfg.Subtitles.Enabled = true
	if err := h.Run(context.Background(), sess); err != nil {
		t.Fatalf("unconfigured OpenSubtitles: %v", err)
	}
	if rec := findGenRecord(sess.Env, "s01e01"); rec == nil || rec.Source != "none" || rec.ValidationResult != "skipped" {
		t.Fatalf("skip not recorded: %+v", rec)
	}
	if got := findGenRecord(sess.Env, "s01e02"); got != nil {
		t.Fatalf("completed subtitle reprocessed: %+v", got)
	}
	// A completed skip record must not trigger another adoption attempt on retry.
	if err := h.Run(context.Background(), sess); err != nil {
		t.Fatalf("resume: %v", err)
	}
}

func TestRunCancelledBeforeSubtitleJob(t *testing.T) {
	sess := newSubtitleTestSession(t, &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Assets:   ripspec.Assets{Ripped: []ripspec.Asset{{EpisodeKey: "main", Path: "/tmp/movie.mkv", Status: ripspec.AssetStatusCompleted}}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := New(&config.Config{Subtitles: config.SubtitlesConfig{Enabled: true}}, nil, nil).Run(ctx, sess); err != context.Canceled {
		t.Fatalf("cancelled stage: %v", err)
	}
}
