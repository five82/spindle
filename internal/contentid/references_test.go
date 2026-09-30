package contentid

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/tmdb"
)

func TestSelectReferenceCanonicalTitleNotPopularityOrAPINumber(t *testing.T) {
	season := &tmdb.Season{Episodes: []tmdb.Episode{{EpisodeNumber: 6, Name: "Lonely Among Us"}, {EpisodeNumber: 7, Name: "Justice"}}}
	candidate := func(id, downloads int, name string) opensubtitles.SubtitleResult {
		return opensubtitles.SubtitleResult{Attributes: opensubtitles.SubtitleAttributes{Language: "en", DownloadCount: downloads,
			Release: "Star Trek", Files: []opensubtitles.SubtitleFile{{FileID: id, FileName: name}}}}
	}
	bad := candidate(1, 999999, "Star.Trek.S01E07.Lonely.Among.Us.srt")
	good := candidate(2, 10, "Star.Trek.1x08.Justice.srt") // Release numbering differs; canonical title wins.
	if got := selectReference([]opensubtitles.SubtitleResult{bad, good}, season, 7); got == nil || got.Files[0].FileID != 2 {
		t.Fatalf("mislabeled popular reference selected: %+v", got)
	}
	for _, name := range []string{"conflicting title", "both titles", "marker only", "title substring", "forced", "non-English", "unknown language", "pack", "no file", "invalid ID"} {
		t.Run(name, func(t *testing.T) {
			c := good
			c.Attributes.Files = append([]opensubtitles.SubtitleFile(nil), good.Attributes.Files...)
			switch name {
			case "conflicting title":
				c = bad
			case "both titles":
				c.Attributes.Release += " Lonely Among Us"
			case "marker only":
				c.Attributes.Files[0].FileName = "S01E07.srt"
			case "title substring":
				c.Attributes.Files[0].FileName = "Injustice.srt"
			case "forced":
				c.Attributes.ForeignPartsOnly = true
			case "non-English":
				c.Attributes.Language = "fr"
			case "unknown language":
				c.Attributes.Language = ""
			case "pack":
				c.Attributes.Files = append(c.Attributes.Files, opensubtitles.SubtitleFile{FileID: 3})
			case "no file":
				c.Attributes.Files = nil
			case "invalid ID":
				c.Attributes.Files[0].FileID = 0
			}
			if got := selectReference([]opensubtitles.SubtitleResult{c}, season, 7); got != nil {
				t.Fatalf("suspect-only search must not fall back: %+v", got)
			}
		})
	}
	low, high, hi := good, candidate(3, 20, "Justice.srt"), candidate(4, 99999, "Justice.srt")
	hi.Attributes.HearingImpaired = true
	for _, results := range [][]opensubtitles.SubtitleResult{{hi, low, high}, {high, low, hi}} {
		if got := selectReference(results, season, 7); got.Files[0].FileID != 3 {
			t.Fatalf("non-HI/download ranking: %+v", got)
		}
	}
	high.Attributes.DownloadCount = low.Attributes.DownloadCount
	if got := selectReference([]opensubtitles.SubtitleResult{high, low}, season, 7); got.Files[0].FileID != 2 {
		t.Fatalf("file-ID tiebreak: %+v", got)
	}
	season.Episodes = append(season.Episodes, tmdb.Episode{EpisodeNumber: 8, Name: "Justice"})
	if got := selectReference([]opensubtitles.SubtitleResult{good}, season, 7); got != nil {
		t.Fatal("duplicate canonical titles cannot establish a unique label")
	}
}

func TestDialogueExcerptBoundsAndPreservesFullEvidence(t *testing.T) {
	cues := []srtutil.Cue{
		{Start: 0, End: 10, Text: "Opening"},
		{Start: 800, End: 850, Text: "<i>Left</i> &amp; {position}boundary"},
		{Start: 1000, End: 1010, Text: "Middle\n dialogue"},
		{Start: 1150, End: 1160, Text: "Right boundary"},
		{Start: 1990, End: 2000, Text: "Ending"},
	}
	before := append([]srtutil.Cue(nil), cues...)
	if got := dialogueExcerpt(cues, 6000); got != "Left & boundary Middle dialogue Right boundary" {
		t.Fatalf("excerpt = %q", got)
	}
	if !reflect.DeepEqual(before, cues) {
		t.Fatal("full-program evidence was mutated")
	}
	for _, cap := range []int{3000, 6000} {
		text := dialogueExcerpt([]srtutil.Cue{{End: 10, Text: strings.Repeat("\u20ac", 3000)}}, cap+1)
		if len(text) != cap || !utf8.ValidString(text) {
			t.Fatalf("UTF-8 byte cap: %d bytes, valid=%v", len(text), utf8.ValidString(text))
		}
	}
	if dialogueExcerpt(nil, 6000) != "" || dialogueExcerpt([]srtutil.Cue{{End: 2000}}, 6000) != "" {
		t.Fatal("empty excerpt invented evidence")
	}
}

func TestReferenceRetryCleanupAndFullSubtitleHandoff(t *testing.T) {
	sess := episodeTestSession(t, "Full source dialogue.")
	h := episodeTestHandler(t, episodeTestClient(t, func(string) (string, float64) { return "E01", 1 }))
	dir, err := sess.StageDir(h.cfg.Paths.StagingDir, "contentid", "references")
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "s01e01-999.srt")
	if err := os.WriteFile(stale, []byte("stale reference"), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(sess.Env.Assets.Transcript[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	words := filepath.Join(filepath.Dir(sess.Env.Assets.Transcript[0].Path), "audio.json")
	if err := os.WriteFile(words, []byte(`{"segments":[{"words":[]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	sess.Logger = slog.New(slog.NewTextHandler(&log, nil))
	if err := h.classifyEpisodes(context.Background(), sess, episodeTestSeason()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale reference survived: %v", err)
	}
	for i := 1; i <= 2; i++ {
		cached, err := os.ReadFile(filepath.Join(h.cfg.OpenSubtitlesCacheDir(), fmt.Sprintf("%d.srt", 100+i)))
		if err != nil {
			t.Fatal(err)
		}
		staged, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("s01e%02d-%d.srt", i, 100+i)))
		if err != nil || !bytes.Equal(cached, staged) {
			t.Fatalf("full reference handoff: %v", err)
		}
	}
	got, err := os.ReadFile(sess.Env.Assets.Transcript[0].Path)
	if err != nil || !bytes.Equal(original, got) {
		t.Fatalf("source transcript altered: %v", err)
	}
	if got, err := os.ReadFile(words); err != nil || string(got) != `{"segments":[{"words":[]}]}` {
		t.Fatalf("word timestamps altered: %s, %v", got, err)
	}
	for _, field := range []string{"decision_type=reference_search", "decision_result=selected", "reference_file_id=101", "file_name=First.srt", "source_byte_cap=6000", "reference_byte_cap=3000", "excerpt_seconds=300"} {
		if !strings.Contains(log.String(), field) {
			t.Errorf("missing reference provenance %s: %s", field, log.String())
		}
	}

	// Configuration failure also invalidates the old handoff and identity.
	h.osClient = nil
	var degraded *stage.ErrDegraded
	if err := h.classifyEpisodes(context.Background(), sess, episodeTestSeason()); !errors.As(err, &degraded) {
		t.Fatalf("unconfigured acquisition: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.srt")); len(matches) != 0 || sess.Env.Episodes[0].Episode != 0 || !sess.Env.Episodes[0].NeedsReview {
		t.Fatalf("stale handoff/identity survived: %v %+v", matches, sess.Env.Episodes[0])
	}
}

func TestReferenceFailureOmitsCandidateWithoutSynopsisRescue(t *testing.T) {
	for _, mode := range []string{"search failure", "suspect only", "empty reference"} {
		t.Run(mode, func(t *testing.T) {
			sess := episodeTestSession(t, "Full source dialogue.")
			season := episodeTestSeason()
			season.Episodes = season.Episodes[:1]
			h := episodeTestHandler(t, episodeTestClient(t, func(string) (string, float64) {
				t.Error("classifier called without references")
				return "E01", 1
			}), season)
			if mode == "empty reference" {
				if err := os.WriteFile(filepath.Join(h.cfg.OpenSubtitlesCacheDir(), "101.srt"), []byte("not an SRT"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if mode == "search failure" {
						http.Error(w, "unavailable", 400)
					} else {
						_, _ = w.Write([]byte(`{"data":[{"attributes":{"language":"en","release":"Wrong title","files":[{"file_id":99}]}}]}`))
					}
				}))
				defer server.Close()
				h.osClient = opensubtitles.New(opensubtitles.Params{APIKey: "test", BaseURL: server.URL}, nil)
			}
			var degraded *stage.ErrDegraded
			if err := h.classifyEpisodes(context.Background(), sess, season); !errors.As(err, &degraded) {
				t.Fatalf("missing reference: %v", err)
			}
			if ep := sess.Env.Episodes[0]; ep.Episode != 0 || !ep.NeedsReview || sess.Env.Attributes.ContentID.ReferenceEpisodes != 0 {
				t.Fatalf("unsafe reference accepted: %+v", ep)
			}
		})
	}
}
