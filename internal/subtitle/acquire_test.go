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
	"github.com/five82/spindle/internal/media/ffprobe"
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
	// Source affinity must not displace an exact TV episode marker.
	sourceResults := []opensubtitles.SubtitleResult{
		{Attributes: opensubtitles.SubtitleAttributes{Release: "Show S01E01 DVDRip", Files: []opensubtitles.SubtitleFile{{FileID: 6}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Release: "Show 2160p UHD", Files: []opensubtitles.SubtitleFile{{FileID: 7}}}},
	}
	if got := rankSearchCandidatesForSource(sourceResults, 1, 1, subtitleSource{class: "uhd"}); got[0].FileID != 6 {
		t.Fatalf("source affinity displaced exact episode marker: %+v", got)
	}

	// Movie mode ignores markers entirely.
	if movie := rankSearchCandidates(results, 0, 0); len(movie) != 5 {
		t.Fatalf("movie mode dropped candidates: %d", len(movie))
	}
}

func TestRankSearchCandidatesSourceAffinity(t *testing.T) {
	uhdResults := []opensubtitles.SubtitleResult{
		{Attributes: opensubtitles.SubtitleAttributes{DownloadCount: 76406, Files: []opensubtitles.SubtitleFile{{FileID: 562828, FileName: "The Sound of Music (EN)"}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Release: "The Sound of Music DVDRip", DownloadCount: 56019, Files: []opensubtitles.SubtitleFile{{FileID: 565122}}}},
		{Attributes: opensubtitles.SubtitleAttributes{DownloadCount: 52318, Files: []opensubtitles.SubtitleFile{{FileID: 567057}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Release: "The Sound of Music 1965 UHD BluRay 2160p HDR10 DV", DownloadCount: 246, Files: []opensubtitles.SubtitleFile{{FileID: 11523270}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Release: "The Sound of Music 1965 REPACK2 2160p UHD Blu-ray Remux", DownloadCount: 153, Files: []opensubtitles.SubtitleFile{{FileID: 11636933}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Release: "The Sound of Music 1965 2160p UHD BluRay", DownloadCount: 92, Files: []opensubtitles.SubtitleFile{{FileID: 11523271}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Release: "The Sound of Music 1965 REPACK2 2160p UHD Blu-ray Remux", DownloadCount: 244, HearingImpaired: true, Files: []opensubtitles.SubtitleFile{{FileID: 11636931}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Release: "The.Sound.of.Music.2160p.UHD.Song.Lyrics.Only", DownloadCount: 1000000, Files: []opensubtitles.SubtitleFile{{FileID: 11523269}}}},
	}
	got := rankSearchCandidatesForSource(uhdResults, 0, 0, subtitleSource{class: "uhd"})
	ids := make([]int, len(got))
	for i, candidate := range got {
		ids[i] = candidate.FileID
	}
	want := []int{11523270, 11636933, 11523271, 11636931, 562828, 567057, 565122, 11523269}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Fatalf("UHD candidate order = %v, want %v", ids, want)
	}

	formatResults := []opensubtitles.SubtitleResult{
		{Attributes: opensubtitles.SubtitleAttributes{Release: "Movie 2160p UHD", DownloadCount: 9000, Files: []opensubtitles.SubtitleFile{{FileID: 10}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Release: "Movie BluRay Remux", DownloadCount: 10, Files: []opensubtitles.SubtitleFile{{FileID: 11}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Release: "Movie DVD", DownloadCount: 20, Files: []opensubtitles.SubtitleFile{{FileID: 12}}}},
		{Attributes: opensubtitles.SubtitleAttributes{Release: "Movie 1080p", DownloadCount: 5, Files: []opensubtitles.SubtitleFile{{FileID: 13}}}},
	}
	for _, tt := range []struct {
		source string
		want   []int
	}{
		{source: "dvd", want: []int{12, 10, 11, 13}},
		{source: "bluray", want: []int{11, 13, 10, 12}},
	} {
		got := rankSearchCandidatesForSource(formatResults, 0, 0, subtitleSource{class: tt.source})
		ids := make([]int, len(got))
		for i, candidate := range got {
			ids[i] = candidate.FileID
		}
		if fmt.Sprint(ids) != fmt.Sprint(tt.want) {
			t.Errorf("%s candidate order = %v, want %v", tt.source, ids, tt.want)
		}
	}
}

func TestRankSearchCandidatesMissingReleaseMetadataIsDeterministic(t *testing.T) {
	results := []opensubtitles.SubtitleResult{
		{Attributes: opensubtitles.SubtitleAttributes{DownloadCount: 100, Files: []opensubtitles.SubtitleFile{{FileID: 20}}}},
		{Attributes: opensubtitles.SubtitleAttributes{DownloadCount: 100, Files: []opensubtitles.SubtitleFile{{FileID: 10}}}},
		{Attributes: opensubtitles.SubtitleAttributes{DownloadCount: 99, Files: []opensubtitles.SubtitleFile{{FileID: 30}}}},
	}
	got := rankSearchCandidatesForSource(results, 0, 0, subtitleSource{class: "uhd"})
	ids := make([]int, len(got))
	for i, candidate := range got {
		ids[i] = candidate.FileID
	}
	if want := []int{10, 20, 30}; fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Fatalf("missing-metadata candidate order = %v, want %v", ids, want)
	}
}

func TestListSubtitleCandidatesUsesActualUHDInput(t *testing.T) {
	origInspect := inspectSubtitleMedia
	t.Cleanup(func() { inspectSubtitleMedia = origInspect })
	inspectSubtitleMedia = func(context.Context, string, string) (*ffprobe.Result, error) {
		return &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video", Width: 3840, Height: 2160}}}, nil
	}
	server := newCandidateSearchServer(t, `{"data":[
		{"id":"a","attributes":{"download_count":999999,"files":[{"file_id":1}]}},
		{"id":"b","attributes":{"release":"Movie DVDRip","download_count":900000,"files":[{"file_id":2}]}},
		{"id":"c","attributes":{"download_count":800000,"files":[{"file_id":3}]}},
		{"id":"d","attributes":{"release":"Movie 2160p UHD","download_count":10,"files":[{"file_id":4}]}},
		{"id":"e","attributes":{"release":"Movie 2160p","download_count":5,"files":[{"file_id":5}]}}
	]}`)
	defer server.Close()

	var logBuf strings.Builder
	h := &Handler{
		cfg:      &config.Config{},
		osClient: opensubtitles.New(opensubtitles.Params{APIKey: "key", BaseURL: server.URL}, discardLogger()),
	}
	sess := newSubtitleTestSession(t, &ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie", ID: 42, DiscSource: "dvd"}})
	sess.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	candidates, reason := h.listSubtitleCandidates(context.Background(), sess, "main", "/rips/movie.mkv")
	if reason != "" {
		t.Fatalf("reason = %q", reason)
	}
	ids := make([]int, len(candidates))
	for i, candidate := range candidates {
		ids[i] = candidate.FileID
	}
	if want := []int{4, 5, 1}; fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Fatalf("daemon candidate order = %v, want %v", ids, want)
	}
	if logs := logBuf.String(); !strings.Contains(logs, "decision_type=subtitle_candidate_ranking") || !strings.Contains(logs, "source_profile=uhd") {
		t.Fatalf("candidate selection decision missing from logs:\n%s", logs)
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
	candidates, reason := h.listSubtitleCandidates(context.Background(), sess, "main", "")
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

	candidates, reason := h.listSubtitleCandidates(context.Background(), sess, "s02_001", "")
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
	if _, reason := h.listSubtitleCandidates(context.Background(), sess, "main", ""); !strings.Contains(reason, "not configured") {
		t.Fatalf("reason = %q", reason)
	}

	h.osClient = opensubtitles.New(opensubtitles.Params{APIKey: "key"}, discardLogger())
	sess = newSubtitleTestSession(t, &ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie"}})
	if _, reason := h.listSubtitleCandidates(context.Background(), sess, "main", ""); !strings.Contains(reason, "TMDB identity") {
		t.Fatalf("reason = %q", reason)
	}

	sess = newSubtitleTestSession(t, &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv", ID: 5},
		Episodes: []ripspec.Episode{{Key: "main", Season: 1, Episode: 1, EpisodeEnd: 2}},
	})
	if _, reason := h.listSubtitleCandidates(context.Background(), sess, "main", ""); !strings.Contains(reason, "multi-episode") {
		t.Fatalf("reason = %q", reason)
	}

	sess = newSubtitleTestSession(t, &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv", ID: 5},
		Episodes: []ripspec.Episode{{Key: "s01_001"}},
	})
	if _, reason := h.listSubtitleCandidates(context.Background(), sess, "s01_001", ""); !strings.Contains(reason, "unresolved") {
		t.Fatalf("reason = %q", reason)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
