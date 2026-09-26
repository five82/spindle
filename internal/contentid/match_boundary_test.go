package contentid

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/tmdb"
)

func TestApplyMatchesFlagsPendingLowConfidenceAndProbableExtra(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Series", "fp")
	if err != nil {
		t.Fatal(err)
	}
	env := ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "tv"}, Episodes: []ripspec.Episode{{Key: "match"}, {Key: "pending"}, {Key: "extra"}}}
	item.RipSpecData, err = env.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateWorkState(item); err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := New(&config.Config{}, nil, nil, nil, nil)
	h.applyMatches(slog.New(slog.NewTextHandler(io.Discard, nil)), sess.Env, 2, &tmdb.Season{Episodes: []tmdb.Episode{{EpisodeNumber: 4, Name: "Fourth", AirDate: "2020-01-01"}}}, []matchResult{{EpisodeKey: "MATCH", TargetEpisode: 4, Confidence: 0.01}}, sess, map[string]struct{}{"extra": {}}, map[string][]matchResult{"PENDING": {{TargetEpisode: 5, Score: 0.8}, {TargetEpisode: 6, Score: 0.7}}})
	episodes := sess.Env.Episodes
	if episodes[0].Episode != 4 || episodes[0].EpisodeTitle != "Fourth" || !episodes[0].NeedsReview {
		t.Fatalf("match: %+v", episodes[0])
	}
	if !episodes[1].NeedsReview || !episodes[2].NeedsReview {
		t.Fatalf("unresolved: %+v", episodes)
	}
	reasons := strings.Join(sess.Item.ReviewReasons(), " ")
	for _, word := range []string{"unresolved", "probable extras", "below confidence"} {
		if !strings.Contains(reasons, word) {
			t.Fatalf("review %q lacks %q", reasons, word)
		}
	}
}
