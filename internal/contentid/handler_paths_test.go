package contentid

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/opensubtitles"
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
		for key, value := range q.Criteria {
			if key != "none" && (!strings.HasPrefix(value, "Reference ") || len(value) > 3000) {
				t.Errorf("choice %s is not bounded reference dialogue: %q", key, value)
			}
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

func episodeTestHandler(t *testing.T, client *llm.Client, catalogs ...*tmdb.Season) *Handler {
	t.Helper()
	season := episodeTestSeason()
	if len(catalogs) > 0 {
		season = catalogs[0]
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := &config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}}
	if err := os.MkdirAll(cfg.OpenSubtitlesCacheDir(), 0700); err != nil {
		t.Fatal(err)
	}
	for i, ep := range season.Episodes {
		data := fmt.Sprintf("1\n00:00:00,000 --> 00:45:00,000\nReference %s dialogue.\n", ep.Name)
		if err := os.WriteFile(filepath.Join(cfg.OpenSubtitlesCacheDir(), fmt.Sprintf("%d.srt", 101+i)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/subtitles" || r.URL.Query().Get("languages") != "en" {
			t.Errorf("unexpected reference request %s", r.URL)
		}
		results := []opensubtitles.SubtitleResult{}
		for i, ep := range season.Episodes {
			if r.URL.Query().Get("episode_number") == fmt.Sprint(ep.EpisodeNumber) {
				results = append(results, opensubtitles.SubtitleResult{Attributes: opensubtitles.SubtitleAttributes{
					Language: "en", Release: ep.Name, Files: []opensubtitles.SubtitleFile{{FileID: 101 + i, FileName: ep.Name + ".srt"}},
				}})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": results})
	}))
	t.Cleanup(server.Close)
	return New(cfg, client, nil, nil, opensubtitles.New(opensubtitles.Params{APIKey: "test", BaseURL: server.URL}, nil))
}

func TestClassifyMiddleExcerptCanonicalIdentity(t *testing.T) {
	text := "Beginning. " + strings.Repeat("Middle dialogue. ", 600) + "Distinctive ending."
	sess := episodeTestSession(t, text, "Other title.")
	sess.Env.Metadata.DiscNumber = 1
	client := episodeTestClient(t, func(got string) (string, float64) {
		if got == text[:6000] {
			return "E02", 0.90
		}
		if got != "Other title." {
			t.Errorf("unexpected transcript excerpt: %d bytes", len(got))
		}
		return "E01", 0.99
	})
	h := episodeTestHandler(t, client)
	season := episodeTestSeason()
	season.Episodes[0].Overview = "" // Synopses are not matching evidence.
	season.Episodes[1].Overview = strings.Repeat("unused", maxEpisodeEvidenceBytes)
	if err := h.classifyEpisodes(context.Background(), sess, season); err != nil {
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
	if !s.Completed || s.MatchedEpisodes != 2 || s.ReferenceEpisodes != 2 || s.ReferenceSource != "opensubtitles" || s.ReviewEpisodes != 0 || !s.SequenceContiguous {
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
			h := episodeTestHandler(t, episodeTestClient(t, func(string) (string, float64) { return tt.winner, tt.p }))
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
	h := episodeTestHandler(t, episodeTestClient(t, func(string) (string, float64) { return "E01", 1 }))
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
	for _, name := range []string{"nil catalog", "empty catalog", "missing name", "duplicate episode", "invalid episode", "too many episodes", "missing transcript", "empty transcript", "unreadable transcript", "missing show", "missing season", "missing client"} {
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
			}
			h := episodeTestHandler(t, client)
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
			h := episodeTestHandler(t, llm.New(config.LLMConfig{APIKey: "test", BaseURL: server.URL}, nil))
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
	h := episodeTestHandler(t, nil)
	if err := h.classifyEpisodes(ctx, sess, episodeTestSeason()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

// Item 4 (TNG S2D1 title 9): E02 at 0.85 with E11 holding 0.14 abstained, but
// the log kept only the winner, so a competing episode was indistinguishable
// from thin evidence without re-transcribing the title.
func TestClassifyAbstentionLogsRunnerUpAndExcerptShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"decision": map[string]any{
			"type": "choice", "choice": "E02", "confidence": 0.85,
			"probabilities": map[string]float64{"E02": 0.85, "E01": 0.14, "none": 0.01},
		}}})
	}))
	t.Cleanup(server.Close)
	sess := episodeTestSession(t, "Source dialogue.")
	var buf bytes.Buffer
	sess.Logger = slog.New(slog.NewJSONHandler(&buf, nil))
	h := episodeTestHandler(t, llm.New(config.LLMConfig{APIKey: "test", BaseURL: server.URL}, nil))
	if err := h.classifyEpisodes(context.Background(), sess, episodeTestSeason()); err != nil {
		t.Fatal(err)
	}
	if ep := sess.Env.Episodes[0]; ep.Episode != 0 || !ep.NeedsReview || ep.MatchProbability != 0.85 {
		t.Fatalf("abstention: %+v", ep)
	}
	var match, reference map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		switch {
		case entry["decision_type"] == "episode_match":
			match = entry
		case entry["decision_type"] == "reference_search" && entry["episode"] == float64(2):
			reference = entry
		}
	}
	if match["candidate"] != "E02" || match["runner_up"] != "E01" || match["runner_up_probability"] != 0.14 || match["none_probability"] != 0.01 {
		t.Fatalf("competitor not logged: %v", match)
	}
	// One 0-2700s cue: midpoint 1350s, the whole 16-byte line in the window.
	if match["excerpt_bytes"] != float64(16) || match["excerpt_midpoint_s"] != float64(1350) || match["excerpt_truncated"] != false {
		t.Fatalf("source excerpt shape not logged: %v", match)
	}
	if reference["excerpt_bytes"] != float64(len("Reference Second dialogue.")) || reference["excerpt_midpoint_s"] != float64(1350) || reference["excerpt_truncated"] != false {
		t.Fatalf("reference excerpt shape not logged: %v", reference)
	}
}

func slotTestSeason() *tmdb.Season {
	season := &tmdb.Season{}
	for n, name := range []string{"One", "Two", "Three", "Four", "Five"} {
		season.Episodes = append(season.Episodes, tmdb.Episode{EpisodeNumber: n + 1, Name: name, Runtime: 45, AirDate: "2001-01-01"})
	}
	return season
}

// Item 4 (TNG S2D1): titles matched E05, E04, E03, E01 and title 9 abstained
// at E02=0.85. The disc's run 1-5 has one open slot and the classifier's
// winner is that slot, so the identity is corroborated rather than reviewed.
func TestClassifySlotCorroboratesBelowThresholdWinner(t *testing.T) {
	sess := episodeTestSession(t, "five", "four", "three", "two", "one")
	sess.Env.Metadata.DiscNumber = 1
	winners := map[string]string{"five": "E05", "four": "E04", "three": "E03", "two": "E02", "one": "E01"}
	h := episodeTestHandler(t, episodeTestClient(t, func(text string) (string, float64) {
		if text == "two" {
			return "E02", 0.85
		}
		return winners[text], 0.99
	}), slotTestSeason())
	if err := h.classifyEpisodes(context.Background(), sess, slotTestSeason()); err != nil {
		t.Fatal(err)
	}
	ep := sess.Env.Episodes[3]
	if ep.Episode != 2 || ep.EpisodeTitle != "Two" || ep.MatchProbability != 0.85 || !ep.SlotCorroborated || ep.NeedsReview {
		t.Fatalf("slot not corroborated: %+v", ep)
	}
	for i, other := range sess.Env.Episodes {
		if i != 3 && (other.SlotCorroborated || other.NeedsReview) {
			t.Fatalf("direct match marked as slot or review: %+v", other)
		}
	}
	if s := sess.Env.Attributes.ContentID; s.MatchedEpisodes != 5 || s.UnresolvedEpisodes != 0 || s.ReviewEpisodes != 0 || !s.SequenceContiguous {
		t.Fatalf("summary %+v", s)
	}
	// A rerun must clear the slot marker along with the identity it justified.
	h = episodeTestHandler(t, episodeTestClient(t, func(string) (string, float64) { return "none", 0.99 }), slotTestSeason())
	if err := h.classifyEpisodes(context.Background(), sess, slotTestSeason()); err != nil {
		t.Fatal(err)
	}
	if ep := sess.Env.Episodes[3]; ep.SlotCorroborated || ep.Episode != 0 {
		t.Fatalf("stale slot identity: %+v", ep)
	}
}

func TestSlotFillsRequireClassifierAgreementInsideTheRun(t *testing.T) {
	details := map[string]tmdb.Episode{}
	for _, ep := range slotTestSeason().Episodes {
		details[fmt.Sprintf("E%02d", ep.EpisodeNumber)] = ep
	}
	low := func(winner string, p float64) titleOutcome {
		return titleOutcome{reason: belowThresholdReason, winner: winner, peak: p}
	}
	abstain := titleOutcome{reason: "no reference has distinctive overlapping dialogue", winner: "none", peak: 0.9}
	for _, tt := range []struct {
		name     string
		accepted []int
		pending  []titleOutcome
		fills    bool
	}{
		{"single interior gap", []int{1, 3, 4, 5}, []titleOutcome{low("E02", 0.85)}, true},
		{"floor is inclusive", []int{1, 3, 4, 5}, []titleOutcome{low("E02", slotProbabilityFloor)}, true},
		{"other abstentions do not block", []int{1, 3, 4, 5}, []titleOutcome{low("E02", 0.85), abstain}, true},
		{"two winners close two gaps", []int{1, 3, 5}, []titleOutcome{low("E02", 0.6), low("E04", 0.7)}, true},
		{"below floor", []int{1, 3, 4, 5}, []titleOutcome{low("E02", 0.49)}, false},
		{"winner is not the gap", []int{1, 3, 4, 5}, []titleOutcome{low("E04", 0.85)}, false},
		{"edge extension is not a slot", []int{1, 2, 3, 4}, []titleOutcome{low("E05", 0.85)}, false},
		{"second gap left open", []int{1, 3, 5}, []titleOutcome{low("E02", 0.85)}, false},
		{"one bad winner blocks all", []int{1, 3, 5}, []titleOutcome{low("E02", 0.85), low("E01", 0.85)}, false},
		{"none abstention never fills", []int{1, 3, 4, 5}, []titleOutcome{abstain}, false},
		{"no accepted run", nil, []titleOutcome{low("E02", 0.85)}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var episodes []ripspec.Episode
			for _, n := range tt.accepted {
				episodes = append(episodes, ripspec.Episode{Episode: n})
			}
			outcomes := make([]titleOutcome, len(episodes))
			var want []int
			for _, o := range tt.pending {
				if tt.fills && o.reason == belowThresholdReason {
					want = append(want, len(episodes))
				}
				episodes = append(episodes, ripspec.Episode{})
				outcomes = append(outcomes, o)
			}
			if got := slotFills(episodes, outcomes, details); !slices.Equal(got, want) {
				t.Fatalf("slotFills = %v, want %v", got, want)
			}
		})
	}
}
