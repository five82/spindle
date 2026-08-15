package subtitle

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestRankSearchCandidatesOrdering(t *testing.T) {
	results := []opensubtitles.SubtitleResult{
		{Attributes: opensubtitles.SubtitleAttributes{Language: "en", DownloadCount: 9000, ForeignPartsOnly: true, Files: []opensubtitles.SubtitleFile{{FileID: 1}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Language: "en", DownloadCount: 8000, HearingImpaired: true, Files: []opensubtitles.SubtitleFile{{FileID: 2}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Language: "en", DownloadCount: 50, Files: []opensubtitles.SubtitleFile{{FileID: 3}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Language: "en", DownloadCount: 700, Files: []opensubtitles.SubtitleFile{{FileID: 4}, {FileID: 0}}}},
	}
	got := rankSearchCandidates(results, 0, 0)
	ids := make([]int, len(got))
	for i, c := range got {
		ids[i] = c.FileID
	}
	want := []int{4, 3, 2}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Fatalf("ranked ids = %v, want %v", ids, want)
	}
}

func TestRankSearchCandidatesEpisodeMarkers(t *testing.T) {
	results := []opensubtitles.SubtitleResult{
		// Mislabeled: metadata says S01E01 but the file name carries a
		// conflicting S01E06 marker -> dropped.
		{Attributes: opensubtitles.SubtitleAttributes{Language: "en", DownloadCount: 9000, Files: []opensubtitles.SubtitleFile{{FileID: 1, FileName: "Star Trek TNG - 1x06 - Where No One Has Gone Before.srt"}}}},
		// No marker at all -> kept, ranked below exact-marker candidates.
		{Attributes: opensubtitles.SubtitleAttributes{Language: "en", DownloadCount: 5000, Files: []opensubtitles.SubtitleFile{{FileID: 2, FileName: "Encounter at Farpoint.srt"}}}},
		// Exact target marker -> ranked first despite fewer downloads.
		{Attributes: opensubtitles.SubtitleAttributes{Language: "en", DownloadCount: 100, Files: []opensubtitles.SubtitleFile{{FileID: 3, FileName: "Star.Trek.TNG.S01E01.BluRay.srt"}}}},
		// Pack upload: the release name spans the range and contains an exact
		// s01e01 marker, but each file's own name reveals its episode.
		{Attributes: opensubtitles.SubtitleAttributes{Language: "en", DownloadCount: 30000, Release: "Star Trek TNG S01E01-06", Files: []opensubtitles.SubtitleFile{
			{FileID: 4, FileName: "StarTrek_TNG_S01E06"},
			{FileID: 5, FileName: "StarTrek_TNG_S01E01"},
		}}},
	}
	got := rankSearchCandidates(results, 1, 1)
	ids := make([]int, len(got))
	for i, c := range got {
		ids[i] = c.FileID
	}
	want := []int{5, 3, 2}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Fatalf("ranked ids = %v, want %v", ids, want)
	}
	// Movie mode ignores markers entirely.
	if movie := rankSearchCandidates(results, 0, 0); len(movie) != 5 {
		t.Fatalf("movie mode dropped candidates: %d", len(movie))
	}
}

func newCandidateSearchServer(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/subtitles" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, payload)
	}))
}

func newSubtitleTestSession(t *testing.T, env *ripspec.Envelope) *stage.Session {
	t.Helper()
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Test Disc", "fingerprint-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.Logger = discardLogger()
	if env.Version == 0 {
		env.Version = ripspec.CurrentVersion
	}
	sess.SetEnvelope(env)
	return sess
}

func TestListSubtitleCandidatesMovie(t *testing.T) {
	server := newCandidateSearchServer(t, `{"data":[
		{"id":"a","attributes":{"language":"en","download_count":10,"files":[{"file_id":11}]}},
		{"id":"b","attributes":{"language":"en","download_count":900,"files":[{"file_id":12}]}},
		{"id":"c","attributes":{"language":"en","download_count":500,"files":[{"file_id":13}]}},
		{"id":"d","attributes":{"language":"en","download_count":400,"files":[{"file_id":14}]}}
	]}`)
	defer server.Close()

	h := &Handler{
		cfg:      &config.Config{},
		osClient: opensubtitles.New(opensubtitles.Params{APIKey: "key", BaseURL: server.URL}, discardLogger()),
	}
	sess := newSubtitleTestSession(t, &ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie", ID: 42}})
	candidates, reason := h.listSubtitleCandidates(context.Background(), sess, "main")
	if reason != "" {
		t.Fatalf("reason = %q", reason)
	}
	if len(candidates) != maxSubtitleCandidates {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].FileID != 12 || candidates[1].FileID != 13 || candidates[2].FileID != 14 {
		t.Fatalf("candidate order = %+v", candidates)
	}
}

func TestListSubtitleCandidatesTVPrefersContentIDReference(t *testing.T) {
	server := newCandidateSearchServer(t, `{"data":[
		{"id":"a","attributes":{"language":"en","download_count":900,"files":[{"file_id":77}]}},
		{"id":"b","attributes":{"language":"en","download_count":500,"files":[{"file_id":88}]}}
	]}`)
	defer server.Close()

	stagingDir := t.TempDir()
	h := &Handler{
		cfg:      &config.Config{Paths: config.PathsConfig{StagingDir: stagingDir}},
		osClient: opensubtitles.New(opensubtitles.Params{APIKey: "key", BaseURL: server.URL}, discardLogger()),
	}
	sess := newSubtitleTestSession(t, &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv", ID: 1396},
		Episodes: []ripspec.Episode{{Key: "s02_001", Season: 2, Episode: 7}},
	})
	root, err := sess.Item.StagingRoot(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	refDir := filepath.Join(root, "contentid", "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	refPath := filepath.Join(refDir, "s02e07-77.srt")
	if err := os.WriteFile(refPath, srtBytes("Reference dialogue."), 0o644); err != nil {
		t.Fatal(err)
	}

	candidates, reason := h.listSubtitleCandidates(context.Background(), sess, "s02_001")
	if reason != "" {
		t.Fatalf("reason = %q", reason)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].Origin != "contentid_reference" || candidates[0].FileID != 77 || candidates[0].LocalPath != refPath {
		t.Fatalf("first candidate = %+v", candidates[0])
	}
	// The search result with the same file ID is deduplicated.
	if candidates[1].FileID != 88 || candidates[1].Origin != "opensubtitles" {
		t.Fatalf("second candidate = %+v", candidates[1])
	}
}

func TestListSubtitleCandidatesSkipReasons(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	sess := newSubtitleTestSession(t, &ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie", ID: 42}})
	if _, reason := h.listSubtitleCandidates(context.Background(), sess, "main"); !strings.Contains(reason, "not configured") {
		t.Fatalf("reason = %q", reason)
	}

	h.osClient = opensubtitles.New(opensubtitles.Params{APIKey: "key"}, discardLogger())
	sess = newSubtitleTestSession(t, &ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie"}})
	if _, reason := h.listSubtitleCandidates(context.Background(), sess, "main"); !strings.Contains(reason, "TMDB identity") {
		t.Fatalf("reason = %q", reason)
	}

	sess = newSubtitleTestSession(t, &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv", ID: 5},
		Episodes: []ripspec.Episode{{Key: "main", Season: 1, Episode: 1, EpisodeEnd: 2}},
	})
	if _, reason := h.listSubtitleCandidates(context.Background(), sess, "main"); !strings.Contains(reason, "multi-episode") {
		t.Fatalf("reason = %q", reason)
	}

	sess = newSubtitleTestSession(t, &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv", ID: 5},
		Episodes: []ripspec.Episode{{Key: "s01_001"}},
	})
	if _, reason := h.listSubtitleCandidates(context.Background(), sess, "s01_001"); !strings.Contains(reason, "unresolved") {
		t.Fatalf("reason = %q", reason)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
