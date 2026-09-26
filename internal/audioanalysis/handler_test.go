package audioanalysis

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestRunRequiresRippedAssets(t *testing.T) {
	h := New(&config.Config{}, nil, nil)
	sess := &stage.Session{Item: &queue.Item{}, Env: &ripspec.Envelope{}, Logger: slog.Default()}
	if err := h.Run(context.Background(), sess); err == nil || err.Error() != "no ripped assets available for analysis" {
		t.Fatalf("Run = %v, want missing ripped assets", err)
	}
}

func TestRunSkippedCommentaryPersistsEmptyAnalysis(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("movie", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	env := ripspec.Envelope{
		Version:  ripspec.CurrentVersion,
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Assets: ripspec.Assets{Ripped: []ripspec.Asset{
			{EpisodeKey: "main", Path: "/rip/main.mkv", Status: ripspec.AssetStatusCompleted},
		}},
	}
	encoded, err := env.Encode()
	if err != nil {
		t.Fatal(err)
	}
	item.RipSpecData = encoded
	if err := store.UpdateWorkState(item); err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		h := New(&config.Config{Commentary: config.CommentaryConfig{Enabled: enabled}}, nil, nil)
		if err := h.Run(context.Background(), sess); err != nil {
			t.Fatalf("Run enabled=%v: %v", enabled, err)
		}
		fresh, err := store.GetByID(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ripspec.Parse(fresh.RipSpecData)
		if err != nil {
			t.Fatal(err)
		}
		if got.Attributes.AudioAnalysis == nil || len(got.Attributes.AudioAnalysis.CommentaryTracks) != 0 {
			t.Fatalf("audio analysis enabled=%v: %+v", enabled, got.Attributes.AudioAnalysis)
		}
	}
}

func TestDetectCommentarySkipsAndFiltersLanguages(t *testing.T) {
	h := New(&config.Config{}, nil, nil)
	sess := &stage.Session{Item: &queue.Item{}, Logger: slog.Default()}
	for _, tc := range []struct {
		name    string
		streams []ffprobe.Stream
		want    int
	}{
		{"no audio", nil, 0},
		{"one audio", []ffprobe.Stream{{CodecType: "audio"}}, 0},
		{"foreign second track", []ffprobe.Stream{
			{CodecType: "audio", Tags: map[string]string{"language": "eng"}},
			{CodecType: "audio", Tags: map[string]string{"language": "jpn"}},
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comms, excluded := h.detectCommentary(context.Background(), sess, &ffprobe.Result{Streams: tc.streams}, "", "", "main")
			if len(comms) != 0 || len(excluded) != tc.want {
				t.Fatalf("commentary=%+v excluded=%+v, want %d excluded", comms, excluded, tc.want)
			}
			if tc.want > 0 && (excluded[0].Index != 1 || excluded[0].Reason != "non-English audio") {
				t.Errorf("excluded track = %+v", excluded[0])
			}
		})
	}
}
