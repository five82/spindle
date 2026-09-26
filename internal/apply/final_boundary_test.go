package apply

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestVerifyFinalOutputsFlagsBadMuxAndPersistsReview(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Movie", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.Env.Version = ripspec.CurrentVersion
	sess.Env.Episodes = []ripspec.Episode{{Key: "main"}}
	sess.Env.Assets.AddAsset(ripspec.AssetKindRipped, ripspec.Asset{EpisodeKey: "main", Path: "source.mkv"})
	sess.Env.Assets.AddAsset(ripspec.AssetKindSubtitled, ripspec.Asset{EpisodeKey: "main", Path: "muxed.mkv", SubtitlesMuxed: true})
	sess.Env.Attributes.SubtitleGenerationResults = []ripspec.SubtitleGenRecord{{EpisodeKey: "main", Source: "opensubtitles", Language: "eng"}}
	installTool(t, "ffprobe", "for last do :; done\ncase \"$last\" in *source*) printf '%s' \"$SOURCE_JSON\";; *) printf '%s' \"$OUTPUT_JSON\";; esac\n")
	t.Setenv("SOURCE_JSON", probeJSON(t, ffprobe.Stream{CodecType: "video", StartTime: "0", Duration: "100"}, ffprobe.Stream{CodecType: "audio", StartTime: "0.5", Duration: "100"}))
	t.Setenv("OUTPUT_JSON", probeJSON(t, ffprobe.Stream{CodecType: "video", StartTime: "0", Duration: "80"}, ffprobe.Stream{CodecType: "audio", StartTime: "0.1", Duration: "80", Tags: map[string]string{"language": "eng"}}))
	verdict, err := verifyFinalOutputs(context.Background(), sess, []finalExpectation{{key: "main", encodedPath: "encoded.mkv", encodedDuration: 100, keptAudio: 2, commentary: []int{0}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Passed || len(verdict.Entries) != 1 || verdict.Entries[0].OutputPath != "muxed.mkv" {
		t.Fatalf("verdict: %+v", verdict)
	}
	failures := strings.Join(verdict.Entries[0].FailedChecks, "; ")
	for _, check := range []string{"av_sync drift", "adopted subtitle expects", "audio stream count", "subtitle mux changed duration", "missing the comment flag"} {
		if !strings.Contains(failures, check) {
			t.Errorf("missing %q in %s", check, failures)
		}
	}
	persisted, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.NeedsReview != 1 || !strings.Contains(persisted.ReviewReason, "final_validation:") {
		t.Fatalf("review state: %+v", persisted)
	}
	// An unavailable source is an incomplete check, not a failed validation.
	sess.Env.Assets = ripspec.Assets{}
	t.Setenv("OUTPUT_JSON", probeJSON(t, ffprobe.Stream{CodecType: "video", StartTime: "0", Duration: "100"}, ffprobe.Stream{CodecType: "audio", StartTime: "0", Duration: "100", Disposition: map[string]int{"default": 1}}))
	verdict, err = verifyFinalOutputs(context.Background(), sess, []finalExpectation{{key: "other", encodedPath: "encoded.mkv", keptAudio: 1}}, 0)
	if err != nil || !verdict.Passed || verdict.Entries[0].AVSync.Error == "" {
		t.Fatalf("unavailable source: %+v %v", verdict, err)
	}
}

func TestCheckAVSyncUnreadableSource(t *testing.T) {
	installTool(t, "ffprobe", "exit 1\n")
	env := &ripspec.Envelope{}
	env.Assets.AddAsset(ripspec.AssetKindRipped, ripspec.Asset{EpisodeKey: "main", Path: "source.mkv"})
	check := checkAVSync(context.Background(), env, "main", avProbe("0", "0"), 0)
	if check.Error == "" || check.SourcePath != "source.mkv" {
		t.Fatalf("check: %+v", check)
	}
}

func TestSubtitleMuxMissingSidecar(t *testing.T) {
	sess := &stage.Session{Env: &ripspec.Envelope{}, Logger: slog.Default()}
	sess.Env.Attributes.SubtitleGenerationResults = []ripspec.SubtitleGenRecord{{EpisodeKey: "main", SubtitlePath: filepath.Join(t.TempDir(), "missing.srt")}}
	if err := New(nil).applySubtitles(context.Background(), sess, "main", "encoded.mkv"); err != nil {
		t.Fatal(err)
	}
}
