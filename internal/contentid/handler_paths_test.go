package contentid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/tmdb"
)

func episodeTestSession(t *testing.T, texts ...string) *stage.Session {
	t.Helper()
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Show", "fp")
	if err != nil {
		t.Fatal(err)
	}
	env := ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "tv", ID: 42, SeasonNumber: 1}}
	for i, text := range texts {
		key := fmt.Sprintf("rip-%d", i+1)
		path := filepath.Join(t.TempDir(), "audio.srt")
		if err := os.WriteFile(path, []byte("1\n00:00:00,000 --> 00:45:00,000\n"+text+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		env.Episodes = append(env.Episodes, ripspec.Episode{Key: key, TitleID: i + 1, RuntimeSeconds: 2700})
		env.Assets.AddAsset(ripspec.AssetKindTranscript, ripspec.Asset{EpisodeKey: key, Path: path, Status: ripspec.AssetStatusCompleted})
	}
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
	return sess
}

func episodeTestSeason() *tmdb.Season {
	return &tmdb.Season{Episodes: []tmdb.Episode{
		{EpisodeNumber: 1, Name: "First", Overview: "A distinctive first adventure.", Runtime: 45, AirDate: "2001-01-01"},
		{EpisodeNumber: 2, Name: "Second", Overview: "A different second adventure.", Runtime: 45, AirDate: "2001-01-08"},
	}}
}

func episodeTestClient(t *testing.T, choose func(string) (string, float64)) *llm.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/systemone" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		var req struct {
			State, Model string
			Questions    map[string]struct {
				Type, Instructions string
				Criteria           map[string]string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		q := req.Questions["decision"]
		if req.Model != "typesafe/jev-1.13" || len(req.Questions) != 1 || q.Type != "choice" || q.Instructions != episodeInstructions || q.Criteria["none"] == "" {
			t.Errorf("invalid request %+v", req)
		}
		winner, p := choose(req.State)
		probabilities := map[string]float64{}
		for key := range q.Criteria {
			probabilities[key] = 0
		}
		probabilities[winner] = p
		remainder := "none"
		if winner == "none" {
			remainder = "E01"
		}
		probabilities[remainder] = 1 - p
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"decision": map[string]any{"type": "choice", "choice": winner, "confidence": 0.01, "probabilities": probabilities}}})
	}))
	t.Cleanup(server.Close)
	return llm.New(config.LLMConfig{APIKey: "test", BaseURL: server.URL}, nil)
}

func TestClassifyFullTranscriptCanonicalIdentity(t *testing.T) {
	text := "Beginning. " + strings.Repeat("Middle dialogue. ", 600) + "Distinctive ending."
	sess := episodeTestSession(t, text, "Other title.")
	sess.Env.Metadata.DiscNumber = 1
	client := episodeTestClient(t, func(got string) (string, float64) {
		if got == text {
			return "E02", 0.90
		}
		if got != "Other title." {
			t.Errorf("full transcript altered or truncated: %d bytes", len(got))
		}
		return "E01", 0.99
	})
	h := New(&config.Config{}, client, nil, nil)
	if err := h.classifyEpisodes(context.Background(), sess, episodeTestSeason()); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{2, 1} {
		ep := sess.Env.Episodes[i]
		if ep.Episode != want || ep.NeedsReview || ep.Key != fmt.Sprintf("rip-%d", i+1) || ep.EpisodeEnd != 0 {
			t.Fatalf("identity: %+v", ep)
		}
	}
	if sess.Env.Episodes[0].EpisodeTitle != "Second" || sess.Env.Episodes[0].EpisodeAirDate != "2001-01-08" {
		t.Fatal("canonical metadata missing")
	}
	s := sess.Env.Attributes.ContentID
	if !s.Completed || s.MatchedEpisodes != 2 || s.ReferenceEpisodes != 2 || s.ReferenceSource != "tmdb" || s.ReviewEpisodes != 0 || !s.SequenceContiguous {
		t.Fatalf("summary %+v", s)
	}
}

func TestClassifyAbstentionsNeverFillHolesOrPreserveStaleMatches(t *testing.T) {
	for _, tt := range []struct {
		name, winner string
		p            float64
	}{{"none", "none", .99}, {"low probability", "E01", .89}} {
		t.Run(tt.name, func(t *testing.T) {
			sess := episodeTestSession(t, "Uncertain evidence.")
			sess.Env.Episodes[0].Episode = 4
			sess.Env.Episodes[0].EpisodeEnd = 5
			sess.Env.Episodes[0].EpisodeTitle = "Stale"
			sess.Env.Episodes[0].MatchProbability = 1
			h := New(&config.Config{}, episodeTestClient(t, func(string) (string, float64) { return tt.winner, tt.p }), nil, nil)
			if err := h.classifyEpisodes(context.Background(), sess, episodeTestSeason()); err != nil {
				t.Fatal(err)
			}
			ep := sess.Env.Episodes[0]
			if ep.Episode != 0 || ep.EpisodeEnd != 0 || ep.EpisodeTitle != "" || !ep.NeedsReview || strings.Contains(ep.ReviewReason, "extra") {
				t.Fatalf("abstention: %+v", ep)
			}
			if sess.Env.Attributes.ContentID.UnresolvedEpisodes != 1 {
				t.Fatal("unresolved count missing")
			}
		})
	}
}

func TestClassifyDuplicateClaimsRouteBothToReview(t *testing.T) {
	sess := episodeTestSession(t, "First recording.", "Second recording.")
	h := New(&config.Config{}, episodeTestClient(t, func(string) (string, float64) { return "E01", 1 }), nil, nil)
	if err := h.classifyEpisodes(context.Background(), sess, episodeTestSeason()); err != nil {
		t.Fatal(err)
	}
	for _, ep := range sess.Env.Episodes {
		if ep.Episode != 1 || !ep.NeedsReview || !strings.Contains(ep.ReviewReason, "overlap") {
			t.Fatalf("collision accepted or forced apart: %+v", ep)
		}
	}
	if sess.Env.Attributes.ContentID.ReviewEpisodes != 2 || sess.Env.Attributes.ContentID.SequenceContiguous {
		t.Fatal("collision summary incorrect")
	}
}

func TestClassifyMissingEvidenceAndCatalog(t *testing.T) {
	for _, name := range []string{"nil catalog", "empty catalog", "missing overview", "missing name", "duplicate episode", "invalid episode", "too many episodes", "missing transcript", "empty transcript", "unreadable transcript", "oversized transcript", "oversized catalog", "missing show", "missing season", "missing client"} {
		t.Run(name, func(t *testing.T) {
			sess := episodeTestSession(t, "Program dialogue.")
			season := episodeTestSeason()
			client := episodeTestClient(t, func(string) (string, float64) { t.Error("ineligible evidence reached API"); return "none", 1 })
			degraded := false
			switch name {
			case "nil catalog":
				season = nil
				degraded = true
			case "empty catalog":
				season = &tmdb.Season{}
				degraded = true
			case "missing overview":
				season.Episodes[1].Overview = ""
				degraded = true
			case "missing name":
				season.Episodes[1].Name = ""
				degraded = true
			case "duplicate episode":
				season.Episodes[1].EpisodeNumber = 1
				degraded = true
			case "invalid episode":
				season.Episodes[1].EpisodeNumber = 0
				degraded = true
			case "too many episodes":
				season.Episodes = make([]tmdb.Episode, 255)
				degraded = true
			case "missing show":
				sess.Env.Metadata.ID = 0
				degraded = true
			case "missing season":
				sess.Env.Metadata.SeasonNumber = 0
				degraded = true
			case "missing client":
				client = nil
				degraded = true
			case "missing transcript":
				sess.Env.Assets.Transcript = nil
			case "unreadable transcript":
				sess.Env.Assets.Transcript[0].Path = filepath.Join(t.TempDir(), "missing")
			case "empty transcript":
				if err := os.WriteFile(sess.Env.Assets.Transcript[0].Path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized transcript":
				if err := os.WriteFile(sess.Env.Assets.Transcript[0].Path, []byte("1\n00:00:00,000 --> 00:45:00,000\n"+strings.Repeat("x", maxEpisodeEvidenceBytes)), 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized catalog":
				season.Episodes[0].Overview = strings.Repeat("x", maxEpisodeEvidenceBytes)
			}
			h := New(&config.Config{}, client, nil, nil)
			err := h.classifyEpisodes(context.Background(), sess, season)
			var de *stage.ErrDegraded
			if errors.As(err, &de) != degraded {
				t.Fatalf("degraded=%v, err=%v", degraded, err)
			}
			if sess.Env.Episodes[0].Episode != 0 || !sess.Env.Episodes[0].NeedsReview {
				t.Fatalf("not reviewed: %+v", sess.Env.Episodes[0])
			}
		})
	}
}

func TestClassifyAPIFailureAndCancellation(t *testing.T) {
	for _, body := range []string{"http error", `{"answers":{"decision":{"type":"choice","choice":"E99","confidence":1,"probabilities":{"E99":1,"none":0}}}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if body == "http error" {
					http.Error(w, "invalid", 400)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			sess := episodeTestSession(t, "Evidence.")
			h := New(&config.Config{}, llm.New(config.LLMConfig{APIKey: "test", BaseURL: server.URL}, nil), nil, nil)
			if err := h.classifyEpisodes(context.Background(), sess, episodeTestSeason()); err != nil {
				t.Fatal(err)
			}
			if ep := sess.Env.Episodes[0]; ep.Episode != 0 || !ep.NeedsReview || !strings.Contains(ep.ReviewReason, "classification failed") {
				t.Fatalf("failure accepted: %+v", ep)
			}
		})
	}
	sess := episodeTestSession(t, "Evidence.")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := New(&config.Config{}, nil, nil, nil)
	if err := h.classifyEpisodes(ctx, sess, episodeTestSeason()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}
