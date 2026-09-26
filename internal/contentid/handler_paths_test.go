package contentid

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestRunDegradesWithoutMatcherAndPersistsSummary(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Show", "fp")
	if err != nil {
		t.Fatal(err)
	}
	env := ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "tv", SeasonNumber: 1}, Episodes: []ripspec.Episode{{Key: "s01_001", TitleID: 1}}}
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
	err = h.Run(context.Background(), sess)
	var degraded *stage.ErrDegraded
	if !errors.As(err, &degraded) || !strings.Contains(err.Error(), "matcher unavailable") {
		t.Fatalf("Run: %v", err)
	}
	fresh, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ripspec.Parse(fresh.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Attributes.ContentID == nil || saved.Attributes.ContentID.Completed || saved.Attributes.ContentID.Method != "whisperx_tfidf_content_matcher" {
		t.Fatalf("summary: %+v", saved.Attributes.ContentID)
	}
	if !strings.Contains(strings.Join(fresh.ReviewReasons(), " "), "matcher unavailable") {
		t.Fatalf("review: %v", fresh.ReviewReasons())
	}
}

func TestGenerateEpisodeFingerprintsSkipsMissingRipsAndHonorsCancel(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Show", "fp")
	if err != nil {
		t.Fatal(err)
	}
	env := ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "tv"}, Episodes: []ripspec.Episode{{Key: "one", TitleID: 1}}}
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
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}}, nil, nil, nil, nil)
	prints, err := h.generateEpisodeFingerprints(context.Background(), sess, sess.Env)
	if err != nil || len(prints) != 0 {
		t.Fatalf("missing rips: %v %v", prints, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.generateEpisodeFingerprints(ctx, sess, sess.Env); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestNewPolicyAndDegradedSummary(t *testing.T) {
	h := New(&config.Config{}, nil, nil, nil, nil)
	if h.policy.LowConfidenceReviewThreshold <= 0 {
		t.Fatalf("policy: %+v", h.policy)
	}
	summary := newDegradedContentIDSummary(h.policy, 3, 7)
	if summary.Completed || summary.TranscribedEpisodes != 3 || summary.ReferenceEpisodes != 7 || summary.EpisodesSynchronized {
		t.Fatalf("summary: %+v", summary)
	}
	if hasSuspectAcceptedMatch([]matchResult{{ReferenceSuspect: false}}) || !hasSuspectAcceptedMatch([]matchResult{{ReferenceSuspect: true}}) {
		t.Fatal("suspect reference detection")
	}
}
