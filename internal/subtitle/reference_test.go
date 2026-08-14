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

func TestTVReferenceTranscriptUsesIdentifiedEpisode(t *testing.T) {
	stagingDir := t.TempDir()
	cfg := &config.Config{Paths: config.PathsConfig{StagingDir: stagingDir}}
	item := &queue.Item{ID: 42, DiscFingerprint: "disc"}
	root, err := item.StagingRoot(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	refDir := filepath.Join(root, "contentid", "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeReferenceSRT(t, filepath.Join(refDir, "s02e07-123.srt"), "Her name is Commander T'Pol.")
	writeReferenceSRT(t, filepath.Join(refDir, "s02e08-456.srt"), "This is the wrong episode.")

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{{Key: "s02_001", Season: 2, Episode: 7}},
	}
	sess := &stage.Session{Item: item, Env: env, Logger: discardLogger()}
	got, reason := (&Handler{cfg: cfg}).tvReferenceTranscript(sess, "s02_001")
	if !strings.Contains(got, "Commander T'Pol") || strings.Contains(got, "wrong episode") {
		t.Fatalf("reference transcript = %q", got)
	}
	if !strings.Contains(reason, "identified TV episode") {
		t.Fatalf("reason = %q", reason)
	}
}

func TestTVReferenceTranscriptCombinesDoubleEpisode(t *testing.T) {
	stagingDir := t.TempDir()
	cfg := &config.Config{Paths: config.PathsConfig{StagingDir: stagingDir}}
	item := &queue.Item{ID: 7}
	root, err := item.StagingRoot(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	refDir := filepath.Join(root, "contentid", "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeReferenceSRT(t, filepath.Join(refDir, "s01e01-10.srt"), "First half dialogue.")
	writeReferenceSRT(t, filepath.Join(refDir, "s01e02-20.srt"), "Second half dialogue.")

	env := &ripspec.Envelope{Episodes: []ripspec.Episode{{Key: "main", Season: 1, Episode: 1, EpisodeEnd: 2}}}
	sess := &stage.Session{Item: item, Env: env, Logger: discardLogger()}
	got, _ := (&Handler{cfg: cfg}).tvReferenceTranscript(sess, "main")
	if !strings.Contains(got, "First half") || !strings.Contains(got, "Second half") {
		t.Fatalf("combined reference = %q", got)
	}
}

func TestSelectAuditReferencePrefersFullNonHI(t *testing.T) {
	results := []opensubtitles.SubtitleResult{
		{Attributes: opensubtitles.SubtitleAttributes{DownloadCount: 5000, ForeignPartsOnly: true, Files: []opensubtitles.SubtitleFile{{FileID: 1}}}},
		{Attributes: opensubtitles.SubtitleAttributes{DownloadCount: 4000, HearingImpaired: true, Files: []opensubtitles.SubtitleFile{{FileID: 2}}}},
		{Attributes: opensubtitles.SubtitleAttributes{DownloadCount: 10, Files: []opensubtitles.SubtitleFile{{FileID: 3}}}},
	}
	choice, ok := selectAuditReference(results)
	if !ok || choice.file.FileID != 3 {
		t.Fatalf("choice = %+v, ok=%t", choice, ok)
	}
}

func TestMovieReferenceTranscriptUsesTMDBSearchAndCache(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/subtitles" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("tmdb_id"); got != "99" {
			t.Fatalf("tmdb_id = %q", got)
		}
		if got := r.URL.Query().Get("type"); got != "movie" {
			t.Fatalf("type = %q", got)
		}
		if r.URL.Query().Has("season_number") || r.URL.Query().Has("episode_number") {
			t.Fatalf("movie search unexpectedly included episode fields: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"ref","attributes":{"language":"en","release":"Movie.2160p","download_count":12,"foreign_parts_only":false,"hearing_impaired":false,"files":[{"file_id":77,"file_name":"movie.srt"}]}}]}`)
	}))
	defer server.Close()

	cfg := &config.Config{Subtitles: config.SubtitlesConfig{OpenSubtitlesLanguages: []string{"en"}}}
	cachePath := filepath.Join(cfg.OpenSubtitlesCacheDir(), "77.srt")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeReferenceSRT(t, cachePath, "The cached movie transcript.")
	client := opensubtitles.New(opensubtitles.Params{APIKey: "key", BaseURL: server.URL}, discardLogger())
	h := &Handler{cfg: cfg, osClient: client}

	got, reason := h.movieReferenceTranscript(context.Background(), discardLogger(), ripspec.Metadata{ID: 99, MediaType: "movie"})
	if got != "The cached movie transcript." {
		t.Fatalf("transcript = %q", got)
	}
	if !strings.Contains(reason, "cached") || !strings.Contains(reason, "file_id=77") {
		t.Fatalf("reason = %q", reason)
	}
}

func TestReferenceTranscriptForPathWithoutTMDBMarker(t *testing.T) {
	text, found, err := ReferenceTranscriptForPath(context.Background(), &config.Config{}, nil, "/library/Movies/Unknown/Unknown.mkv")
	if err != nil || found || text != "" {
		t.Fatalf("text=%q found=%t err=%v", text, found, err)
	}
}

func TestReferenceTranscriptForMoviePath(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tmdb_id") != "27205" || r.URL.Query().Get("type") != "movie" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"ref","attributes":{"language":"en","download_count":12,"files":[{"file_id":90,"file_name":"movie.srt"}]}}]}`)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cachePath := filepath.Join(cfg.OpenSubtitlesCacheDir(), "90.srt")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeReferenceSRT(t, cachePath, "Dreams feel real while we're in them.")
	client := opensubtitles.New(opensubtitles.Params{APIKey: "key", BaseURL: server.URL}, discardLogger())
	path := "/library/Movies/Inception (2010) [tmdbid-27205]/Inception (2010) [tmdbid-27205].mkv"

	text, found, err := ReferenceTranscriptForPath(context.Background(), cfg, client, path)
	if err != nil || !found || !strings.Contains(text, "Dreams feel real") {
		t.Fatalf("text=%q found=%t err=%v", text, found, err)
	}
}

func TestReferenceTranscriptForTVEpisodePath(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("tmdb_id") != "1396" || query.Get("type") != "episode" || query.Get("season_number") != "2" || query.Get("episode_number") != "7" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"ref","attributes":{"language":"en","download_count":12,"files":[{"file_id":91,"file_name":"episode.srt"}]}}]}`)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cachePath := filepath.Join(cfg.OpenSubtitlesCacheDir(), "91.srt")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeReferenceSRT(t, cachePath, "The identified episode transcript.")
	client := opensubtitles.New(opensubtitles.Params{APIKey: "key", BaseURL: server.URL}, discardLogger())
	path := "/library/TV/Breaking Bad (2008) [tmdbid-1396]/Season 02/Breaking Bad - S02E07.mkv"

	text, found, err := ReferenceTranscriptForPath(context.Background(), cfg, client, path)
	if err != nil || !found || text != "The identified episode transcript." {
		t.Fatalf("text=%q found=%t err=%v", text, found, err)
	}
}

func TestEpisodePathPatternParsesDoubleEpisode(t *testing.T) {
	match := episodePathPattern.FindStringSubmatch("Show - S01E01-E02.mkv")
	if len(match) == 0 || match[1] != "01" || match[2] != "01" || match[3] != "02" {
		t.Fatalf("episode match = %#v", match)
	}
}

func TestReferenceTranscriptForPathRejectsSeasonMismatch(t *testing.T) {
	path := "/library/TV/Show [tmdbid-1]/Season 02/Show - S03E04.mkv"
	_, found, err := ReferenceTranscriptForPath(context.Background(), &config.Config{}, nil, path)
	if !found || err == nil || !strings.Contains(err.Error(), "inconsistent") {
		t.Fatalf("found=%t err=%v", found, err)
	}
}

func TestReferenceTranscriptForPathRejectsConflictingIDs(t *testing.T) {
	path := "/library/Movies/Film [tmdbid-1]/Film [tmdbid-2].mkv"
	_, found, err := ReferenceTranscriptForPath(context.Background(), &config.Config{}, nil, path)
	if !found || err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("found=%t err=%v", found, err)
	}
}

func writeReferenceSRT(t *testing.T, path, text string) {
	t.Helper()
	content := "1\n00:10:00,000 --> 00:10:02,000\n" + text + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
