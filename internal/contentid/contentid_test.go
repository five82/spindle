package contentid

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/tmdb"
)

func TestPersistContentIDResultsPreservesConcurrentEncodedAsset(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Test", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	env := ripspec.Envelope{
		Version:  ripspec.CurrentVersion,
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{{Key: "s01_001", TitleID: 1}},
	}
	item.RipSpecData, err = env.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateWorkState(item); err != nil {
		t.Fatal(err)
	}

	contentSession, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	encodeItem, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	encodeSession, err := stage.NewSession(context.Background(), store, encodeItem, nil)
	if err != nil {
		t.Fatal(err)
	}

	contentSession.Env.Episodes[0].Episode = 5
	contentSession.Env.Episodes[0].AppendReviewReason("Episode ID: content review")
	contentSession.Env.Attributes.ContentID = &ripspec.ContentIDSummary{Completed: true, MatchedEpisodes: 1}
	contentSession.AddReviewReason("content ID review")
	if err := encodeSession.SaveAssetSuccess(ripspec.AssetKindEncoded, ripspec.Asset{EpisodeKey: "s01_001", Path: "encoded.mkv"}); err != nil {
		t.Fatal(err)
	}
	if err := encodeSession.MergeSave(func(env *ripspec.Envelope) error {
		env.Episodes[0].AppendReviewReason("Encoding validation failed")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := persistContentIDResults(contentSession); err != nil {
		t.Fatal(err)
	}
	if err := persistContentIDResults(contentSession); err != nil {
		t.Fatal(err)
	}

	fresh, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ripspec.Parse(fresh.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	if asset, ok := got.Assets.FindAsset(ripspec.AssetKindEncoded, "s01_001"); !ok || asset.Path != "encoded.mkv" {
		t.Fatalf("encoded asset = %#v, found=%v", asset, ok)
	}
	if got.Episodes[0].Episode != 5 || got.Attributes.ContentID == nil || !got.Attributes.ContentID.Completed {
		t.Fatalf("content ID result not persisted: %#v", got)
	}
	if !got.Episodes[0].NeedsReview || got.Episodes[0].ReviewReason != "Encoding validation failed; Episode ID: content review" {
		t.Fatalf("concurrent review lost or duplicated: %+v", got.Episodes[0])
	}
	if reasons := fresh.ReviewReasons(); len(reasons) != 1 || reasons[0] != "content ID review" {
		t.Fatalf("review reasons = %#v", reasons)
	}
}

func TestRunSkipsNonTVContent(t *testing.T) {
	env := ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "unknown"}}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("Test", "fp1")
	if err != nil {
		t.Fatalf("new disc: %v", err)
	}
	item.RipSpecData = string(data)
	if err := store.UpdateWorkState(item); err != nil {
		t.Fatalf("update work state: %v", err)
	}

	h := &Handler{}
	ctx := context.Background()
	sess, err := stage.NewSession(ctx, store, item, nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := h.Run(ctx, sess); err != nil {
		t.Fatalf("Run returned error for non-TV content: %v", err)
	}
}

func TestStructuralReviewReasons(t *testing.T) {
	tests := []struct {
		name        string
		episodes    []ripspec.Episode
		discNumber  int
		wantReasons []string
	}{
		{
			name: "disc 1 matched subset starts above episode 1",
			episodes: []ripspec.Episode{
				{Episode: 2},
				{Episode: 3},
				{Episode: 4},
			},
			discNumber:  1,
			wantReasons: []string{"disc 1 matched subset starts at episode 2"},
		},
		{
			name: "disc 1 starts at episode 1",
			episodes: []ripspec.Episode{
				{Episode: 1},
				{Episode: 2},
				{Episode: 3},
			},
			discNumber:  1,
			wantReasons: []string{},
		},
		{
			name: "disc 2 does not require starting at episode 1",
			episodes: []ripspec.Episode{
				{Episode: 5},
				{Episode: 6},
			},
			discNumber:  2,
			wantReasons: []string{},
		},
		{
			name: "episode range expansion covers the gap",
			episodes: []ripspec.Episode{
				{Episode: 1, EpisodeEnd: 2},
				{Episode: 3},
			},
			discNumber:  1,
			wantReasons: []string{},
		},
		{
			name: "fragmented subset with multiple gaps",
			episodes: []ripspec.Episode{
				{Episode: 1},
				{Episode: 3},
				{Episode: 5},
				{Episode: 7},
			},
			discNumber:  1,
			wantReasons: []string{"accepted episode subset is fragmented"},
		},
		{
			name: "all episodes unassigned",
			episodes: []ripspec.Episode{
				{Episode: 0},
				{Episode: 0},
			},
			discNumber:  1,
			wantReasons: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			season := &tmdb.Season{}
			for n := 1; n <= 8; n++ {
				season.Episodes = append(season.Episodes, tmdb.Episode{EpisodeNumber: n, Runtime: 45})
			}
			for i := range tt.episodes {
				tt.episodes[i].RuntimeSeconds = 45 * 60 * (tt.episodes[i].EpisodeLast() - tt.episodes[i].Episode + 1)
			}
			got := structuralReviewReasons(tt.episodes, tt.discNumber, season)
			if !reflect.DeepEqual(got, tt.wantReasons) {
				t.Fatalf("structuralReviewReasons() = %v, want %v", got, tt.wantReasons)
			}
		})
	}
}
