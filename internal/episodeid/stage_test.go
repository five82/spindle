package episodeid

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"spindle/internal/contentid"
	"spindle/internal/identification/tmdb"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/subtitles"
	"spindle/internal/subtitles/opensubtitles"
	"spindle/internal/testsupport"
)

type fakeSubtitleGenerator struct {
	content string
}

func (f fakeSubtitleGenerator) Generate(_ context.Context, req subtitles.GenerateRequest) (subtitles.GenerateResult, error) {
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return subtitles.GenerateResult{}, err
	}
	path := filepath.Join(req.OutputDir, req.BaseName+".srt")
	if err := os.WriteFile(path, []byte(f.content), 0o644); err != nil {
		return subtitles.GenerateResult{}, err
	}
	return subtitles.GenerateResult{SubtitlePath: path}, nil
}

type fakeOpenSubtitles struct {
	fileID int64
}

func (f fakeOpenSubtitles) Search(_ context.Context, req opensubtitles.SearchRequest) (opensubtitles.SearchResponse, error) {
	if req.Episode <= 0 {
		return opensubtitles.SearchResponse{}, nil
	}
	return opensubtitles.SearchResponse{
		Subtitles: []opensubtitles.Subtitle{
			{FileID: f.fileID, Language: "en", FeatureTitle: req.Query},
		},
		Total: 1,
	}, nil
}

func (f fakeOpenSubtitles) Download(_ context.Context, _ int64, _ opensubtitles.DownloadOptions) (opensubtitles.DownloadResult, error) {
	return opensubtitles.DownloadResult{}, nil
}

type fakeSeasonFetcher struct {
	season *tmdb.SeasonDetails
}

func (f fakeSeasonFetcher) GetSeasonDetails(_ context.Context, _ int64, _ int) (*tmdb.SeasonDetails, error) {
	return f.season, nil
}

func TestPrepareSkipsMovie(t *testing.T) {
	item := &queue.Item{
		DiscTitle:    "Movie Title",
		MetadataJSON: `{"media_type":"movie"}`,
	}
	id := &EpisodeIdentifier{}

	if err := id.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if item.ProgressMessage != "Skipped (movie content)" {
		t.Fatalf("unexpected progress message: %q", item.ProgressMessage)
	}
	if item.ProgressPercent != 100 {
		t.Fatalf("expected progress percent 100, got %v", item.ProgressPercent)
	}
}

func TestExecuteSkipsMovie(t *testing.T) {
	item := &queue.Item{
		DiscTitle:    "Movie Title",
		MetadataJSON: `{"media_type":"movie"}`,
	}
	id := &EpisodeIdentifier{}

	if err := id.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if item.ProgressMessage != "Skipped (movie content)" {
		t.Fatalf("unexpected progress message: %q", item.ProgressMessage)
	}
}

func TestExecuteSkipsMissingRipSpec(t *testing.T) {
	item := &queue.Item{
		DiscTitle: "Show Disc",
	}
	id := &EpisodeIdentifier{}

	if err := id.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if item.ProgressMessage != "Skipped (no rip spec)" {
		t.Fatalf("unexpected progress message: %q", item.ProgressMessage)
	}
}

func TestExecuteSkipsInvalidRipSpec(t *testing.T) {
	item := &queue.Item{
		DiscTitle:   "Show Disc",
		RipSpecData: "{not-json}",
	}
	id := &EpisodeIdentifier{}

	if err := id.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if item.ProgressMessage != "Skipped (invalid rip spec)" {
		t.Fatalf("unexpected progress message: %q", item.ProgressMessage)
	}
}

func TestExecuteSkipsNoEpisodes(t *testing.T) {
	env := ripspec.Envelope{}
	encoded, err := env.Encode()
	if err != nil {
		t.Fatalf("encode rip spec: %v", err)
	}
	item := &queue.Item{
		DiscTitle:   "Show Disc",
		RipSpecData: encoded,
	}
	id := &EpisodeIdentifier{}

	if err := id.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if item.ProgressMessage != "Skipped (no episodes)" {
		t.Fatalf("unexpected progress message: %q", item.ProgressMessage)
	}
}

func TestExecuteSkipsWhenOpenSubtitlesDisabled(t *testing.T) {
	env := ripspec.Envelope{
		Episodes: []ripspec.Episode{{Key: "s01e01"}},
	}
	encoded, err := env.Encode()
	if err != nil {
		t.Fatalf("encode rip spec: %v", err)
	}
	cfg := testsupport.NewConfig(t)
	cfg.Subtitles.OpenSubtitlesEnabled = false
	item := &queue.Item{
		DiscTitle:   "Show Disc",
		RipSpecData: encoded,
	}
	id := &EpisodeIdentifier{cfg: cfg}

	if err := id.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if item.ProgressMessage != "Skipped (OpenSubtitles disabled)" {
		t.Fatalf("unexpected progress message: %q", item.ProgressMessage)
	}
}

func TestExecuteMatchesEpisodes(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	cfg.Subtitles.OpenSubtitlesEnabled = true
	cfg.Subtitles.OpenSubtitlesLanguages = []string{"en"}
	cfg.Paths.OpenSubtitlesCacheDir = filepath.Join(t.TempDir(), "os-cache")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	matcher := contentid.NewMatcher(
		cfg,
		logger,
		contentid.WithSubtitleGenerator(fakeSubtitleGenerator{content: sampleSRT()}),
		contentid.WithOpenSubtitlesClient(fakeOpenSubtitles{fileID: 1}),
		contentid.WithSeasonFetcher(fakeSeasonFetcher{
			season: &tmdb.SeasonDetails{
				SeasonNumber: 1,
				Episodes: []tmdb.Episode{
					{SeasonNumber: 1, EpisodeNumber: 1, Name: "Pilot", AirDate: "2010-01-01"},
				},
			},
		}),
		contentid.WithLanguages([]string{"en"}),
	)

	cache, err := opensubtitles.NewCache(cfg.Paths.OpenSubtitlesCacheDir, logger)
	if err != nil {
		t.Fatalf("create cache: %v", err)
	}
	_, err = cache.Store(opensubtitles.CacheEntry{
		FileID:       1,
		Language:     "en",
		FileName:     "pilot.srt",
		DownloadURL:  "file://local",
		TMDBID:       100,
		ParentTMDBID: 100,
		Season:       1,
		Episode:      1,
	}, []byte(sampleSRT()))
	if err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	rippedPath := filepath.Join(t.TempDir(), "episode1.mkv")
	if err := os.WriteFile(rippedPath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write ripped file: %v", err)
	}

	env := ripspec.Envelope{
		Titles: []ripspec.Title{{ID: 1, Name: "Disc Title"}},
		Episodes: []ripspec.Episode{{
			Key:     "s01e01",
			TitleID: 1,
		}},
		Assets: ripspec.Assets{
			Ripped: []ripspec.Asset{{EpisodeKey: "s01e01", TitleID: 1, Path: rippedPath}},
		},
	}
	encoded, err := env.Encode()
	if err != nil {
		t.Fatalf("encode rip spec: %v", err)
	}

	meta, err := json.Marshal(map[string]any{
		"id":            100,
		"media_type":    "tv",
		"show_title":    "Test Show",
		"season_number": 1,
	})
	if err != nil {
		t.Fatalf("encode metadata: %v", err)
	}

	item := &queue.Item{
		ID:              1,
		DiscFingerprint: "fp-test",
		DiscTitle:       "Test Show Disc",
		MetadataJSON:    string(meta),
		RipSpecData:     encoded,
	}

	id := &EpisodeIdentifier{cfg: cfg, matcher: matcher}
	id.SetLogger(logger)

	if err := id.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if item.ProgressMessage != "Episodes correlated with OpenSubtitles" {
		t.Fatalf("unexpected progress message: %q", item.ProgressMessage)
	}
	updated, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		t.Fatalf("parse updated rip spec: %v", err)
	}
	if len(updated.Episodes) != 1 || updated.Episodes[0].Episode != 1 || updated.Episodes[0].Season != 1 {
		t.Fatalf("episode metadata not updated: %+v", updated.Episodes)
	}
	if updated.Episodes[0].OutputBasename == "" {
		t.Fatalf("expected output basename to be set")
	}
	if updated.Attributes == nil || updated.Attributes["content_id_method"] != "whisperx_opensubtitles" {
		t.Fatalf("expected content id attributes to be recorded")
	}
	metadata := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
	if len(metadata.EpisodeNumbers) != 1 || metadata.EpisodeNumbers[0] != 1 {
		t.Fatalf("expected metadata episode numbers to be updated, got %+v", metadata.EpisodeNumbers)
	}
}

func sampleSRT() string {
	return "1\n00:00:00,000 --> 00:00:01,000\nHello world\n\n"
}
