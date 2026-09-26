package apply

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestApplyRunWithoutSubtitlesPersistsFinalValidation(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Movie", "fp")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.Env.Version = ripspec.CurrentVersion
	sess.Env.Episodes = []ripspec.Episode{{Key: "main"}}
	encoded := filepath.Join(t.TempDir(), "encoded.mkv")
	if err := os.WriteFile(encoded, []byte("encoded"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess.Env.Assets.AddAsset(ripspec.AssetKindEncoded, ripspec.Asset{EpisodeKey: "main", Path: encoded, Status: ripspec.AssetStatusCompleted})
	sess.Env.Assets.AddAsset(ripspec.AssetKindRipped, ripspec.Asset{EpisodeKey: "main", Path: "ripped.mkv", Status: ripspec.AssetStatusCompleted})
	installTool(t, "ffprobe", "printf '%s' \"$PROBE_JSON\"\n")
	t.Setenv("PROBE_JSON", probeJSON(t,
		ffprobe.Stream{CodecType: "video", StartTime: "0", Duration: "100"},
		ffprobe.Stream{CodecType: "audio", CodecName: "aac", Channels: 2, StartTime: "0.5", Duration: "100", Tags: map[string]string{"language": "eng"}, Disposition: map[string]int{"default": 1}},
	))
	if err := New(&config.Config{}).Run(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	env, err := ripspec.Parse(got.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	if env.Attributes.FinalValidation == nil || !env.Attributes.FinalValidation.Passed || len(env.Attributes.FinalValidation.Entries) != 1 {
		t.Fatalf("verdict: %+v", env.Attributes.FinalValidation)
	}
	if env.Attributes.AudioAnalysis == nil || env.Attributes.AudioAnalysis.PrimaryTrack.Index != 0 {
		t.Fatalf("audio analysis: %+v", env.Attributes.AudioAnalysis)
	}
	if got.NeedsReview != 0 {
		t.Fatalf("unexpected review: %s", got.ReviewReason)
	}
}
