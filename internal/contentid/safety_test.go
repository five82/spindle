package contentid

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/tmdb"
)

func TestStructuralSafetyCachedTVCorpus(t *testing.T) {
	// Hand-reviewed assignments and actual source/TMDB runtimes from the
	// 2026-09-28 evaluation: all 32 titles, including the 91-minute Farpoint E1.
	data, err := os.ReadFile("testdata/cached-tv-safety.json")
	if err != nil {
		t.Fatal(err)
	}
	var discs []struct {
		Name       string
		DiscNumber int `json:"disc_number"`
		Season     tmdb.Season
		Episodes   []ripspec.Episode
	}
	if err := json.Unmarshal(data, &discs); err != nil {
		t.Fatal(err)
	}
	titles := 0
	for _, disc := range discs {
		titles += len(disc.Episodes)
		t.Run(disc.Name, func(t *testing.T) {
			if reasons := structuralReviewReasons(disc.Episodes, disc.DiscNumber, &disc.Season); len(reasons) != 0 {
				t.Fatalf("reviewed cached disc rejected: %v", reasons)
			}
		})
	}
	if len(discs) != 6 || titles != 32 {
		t.Fatalf("corpus has %d discs / %d titles", len(discs), titles)
	}
}

func TestStructuralSafetyIndependentOfMatcherConfidence(t *testing.T) {
	season := &tmdb.Season{Episodes: []tmdb.Episode{
		{EpisodeNumber: 1, Runtime: 45}, {EpisodeNumber: 2, Runtime: 45},
		{EpisodeNumber: 3, Runtime: 45}, {EpisodeNumber: 4, Runtime: 45},
		{EpisodeNumber: 5}, {EpisodeNumber: 6, Runtime: 10},
	}}
	for _, tt := range []struct {
		name     string
		episodes []ripspec.Episode
		want     string
	}{
		{"duplicate assignments", []ripspec.Episode{{Episode: 2, RuntimeSeconds: 2700}, {Episode: 2, RuntimeSeconds: 2700}}, "assignments overlap"},
		{"overlapping ranges", []ripspec.Episode{{Episode: 1, EpisodeEnd: 2, RuntimeSeconds: 5400}, {Episode: 2, RuntimeSeconds: 2700}}, "assignments overlap"},
		{"range overlapping range", []ripspec.Episode{{Episode: 1, EpisodeEnd: 3, RuntimeSeconds: 8100}, {Episode: 2, EpisodeEnd: 4, RuntimeSeconds: 8100}}, "assignments overlap"},
		{"explicit nonoverlapping ranges", []ripspec.Episode{{Episode: 1, EpisodeEnd: 2, RuntimeSeconds: 5400}, {Episode: 3, EpisodeEnd: 4, RuntimeSeconds: 5400}}, ""},
		{"combined program matched to one half", []ripspec.Episode{{Episode: 2, RuntimeSeconds: 5460}}, "possible partial or composite episode"},
		{"partial program", []ripspec.Episode{{Episode: 2, RuntimeSeconds: 1350}}, "possible partial or composite episode"},
		{"above upper ratio bound", []ripspec.Episode{{Episode: 2, RuntimeSeconds: 3376}}, "possible partial or composite episode"},
		{"below lower ratio bound", []ripspec.Episode{{Episode: 2, RuntimeSeconds: 2024}}, "possible partial or composite episode"},
		{"at upper ratio bound", []ripspec.Episode{{Episode: 2, RuntimeSeconds: 3375}}, ""},
		{"at lower ratio bound", []ripspec.Episode{{Episode: 2, RuntimeSeconds: 2025}}, ""},
		{"absolute tolerance for short programs", []ripspec.Episode{{Episode: 6, RuntimeSeconds: 900}}, ""},
		{"outside absolute tolerance", []ripspec.Episode{{Episode: 6, RuntimeSeconds: 901}}, "possible partial or composite episode"},
		{"missing source runtime", []ripspec.Episode{{Episode: 2}}, "runtime unavailable"},
		{"missing TMDB runtime", []ripspec.Episode{{Episode: 5, RuntimeSeconds: 2700}}, "runtime unavailable"},
		{"unknown canonical episode", []ripspec.Episode{{Episode: 7, RuntimeSeconds: 2700}}, "runtime unavailable"},
		{"range includes missing runtime", []ripspec.Episode{{Episode: 4, EpisodeEnd: 5, RuntimeSeconds: 5400}}, "runtime unavailable"},
		{"unresolved is not silently assigned", []ripspec.Episode{{Episode: 0, RuntimeSeconds: 2700}}, ""},
		{"canonical order differs from physical order", []ripspec.Episode{{Episode: 3, RuntimeSeconds: 2700}, {Episode: 1, RuntimeSeconds: 2700}, {Episode: 2, RuntimeSeconds: 2700}}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for i := range tt.episodes {
				tt.episodes[i].MatchProbability = 1
			}
			before := append([]ripspec.Episode(nil), tt.episodes...)
			reasons := structuralReviewReasons(tt.episodes, 2, season)
			if tt.want == "" && len(reasons) > 0 || tt.want != "" && !strings.Contains(strings.Join(reasons, "; "), tt.want) {
				t.Fatalf("reasons = %v; want %q", reasons, tt.want)
			}
			if !reflect.DeepEqual(before, tt.episodes) {
				t.Fatal("safety check mutated canonical assignments")
			}
		})
	}
}

func TestClassifyEpisodesPersistsStructuralSafety(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		canonicalOpenerRuntime int
		wantReview             bool
	}{
		{"canonical long E1", 90, false},
		{"combined program cannot be a single 45 minute episode", 45, true},
		{"missing runtime cannot clear composite concern", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sess := episodeTestSession(t, "E01", "E02", "E03")
			if err := sess.MergeSave(func(env *ripspec.Envelope) error {
				env.Metadata.DiscNumber = 1
				env.Episodes[0].RuntimeSeconds = 5460
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			sess.Logger = slog.New(slog.NewTextHandler(&logs, nil))
			season := &tmdb.Season{Episodes: []tmdb.Episode{
				{EpisodeNumber: 1, Name: "Canonical opener", Overview: "First adventure.", Runtime: tt.canonicalOpenerRuntime},
				{EpisodeNumber: 2, Name: "Canonical second", Overview: "Second adventure.", Runtime: 45},
				{EpisodeNumber: 3, Name: "Canonical third", Overview: "Third adventure.", Runtime: 45},
			}}
			h := New(&config.Config{}, episodeTestClient(t, func(text string) (string, float64) { return text, 1 }), nil, nil)
			if err := h.classifyEpisodes(context.Background(), sess, season); err != nil {
				t.Fatal(err)
			}
			fresh, err := sess.Store.GetByID(sess.Item.ID)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := ripspec.Parse(fresh.RipSpecData)
			if err != nil {
				t.Fatal(err)
			}
			for i, ep := range saved.Episodes {
				if ep.Key != sess.Env.Episodes[i].Key || ep.Episode != i+1 || ep.EpisodeEnd != 0 || ep.EpisodeTitle != season.Episodes[i].Name {
					t.Fatalf("canonical numbering or metadata changed: %+v", ep)
				}
				if ep.NeedsReview != tt.wantReview {
					t.Fatalf("persisted review = %v: %+v", ep.NeedsReview, ep)
				}
				if tt.wantReview && !strings.Contains(ep.ReviewReason, "episode set unsafe") {
					t.Fatalf("review reason missing: %+v", ep)
				}
			}
			if (fresh.NeedsReview == 1) != tt.wantReview {
				t.Fatalf("item review = %d, reasons %v", fresh.NeedsReview, fresh.ReviewReasons())
			}
			if tt.wantReview {
				for _, field := range []string{"episode set requires review", "decision_type=", "decision_result=resolved_episodes_routed_to_review", "decision_reason="} {
					if !strings.Contains(logs.String(), field) {
						t.Fatalf("missing %s in logs: %s", field, logs.String())
					}
				}
			}
		})
	}
}
