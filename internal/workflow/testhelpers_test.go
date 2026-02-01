package workflow_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spindle/internal/disc"
	"spindle/internal/encoding"
	"spindle/internal/identification/tmdb"
	"spindle/internal/media/ffprobe"
	"spindle/internal/notifications"
	"spindle/internal/organizer"
	"spindle/internal/ripping"
	"spindle/internal/services/drapto"
	"spindle/internal/services/jellyfin"
	"spindle/internal/services/makemkv"
)

type stubNotifier struct {
	ripStarts           []string
	ripCompletes        []string
	encodeCompletes     []string
	organizeCompletes   []string
	processingCompletes []string
}

func (s *stubNotifier) Publish(ctx context.Context, event notifications.Event, payload notifications.Payload) error {
	var title string
	if payload != nil {
		if v, ok := payload["discTitle"].(string); ok {
			title = v
		} else if v, ok := payload["title"].(string); ok {
			title = v
		} else if v, ok := payload["mediaTitle"].(string); ok {
			title = v
		}
	}
	switch event {
	case notifications.EventRipStarted:
		s.ripStarts = append(s.ripStarts, title)
	case notifications.EventRipCompleted:
		s.ripCompletes = append(s.ripCompletes, title)
	case notifications.EventEncodingCompleted:
		s.encodeCompletes = append(s.encodeCompletes, title)
	case notifications.EventOrganizationCompleted:
		s.organizeCompletes = append(s.organizeCompletes, title)
	case notifications.EventProcessingCompleted:
		s.processingCompletes = append(s.processingCompletes, title)
	}
	return nil
}

func stubValidationProbes(t *testing.T) {
	t.Helper()
	probeResult := ffprobe.Result{
		Streams: []ffprobe.Stream{
			{CodecType: "video"},
			{CodecType: "audio"},
		},
		Format: ffprobe.Format{
			Duration: "5400",
			Size:     "20971520",
			BitRate:  "6000000",
		},
	}
	restoreRipper := ripping.SetProbeForTests(func(ctx context.Context, binary, path string) (ffprobe.Result, error) {
		return probeResult, nil
	})
	restoreEncoder := encoding.SetProbeForTests(func(ctx context.Context, binary, path string) (ffprobe.Result, error) {
		return probeResult, nil
	})
	restoreOrganizer := organizer.SetProbeForTests(func(ctx context.Context, binary, path string) (ffprobe.Result, error) {
		return probeResult, nil
	})
	t.Cleanup(func() {
		restoreOrganizer()
		restoreEncoder()
		restoreRipper()
	})
}

type stubDraptoClient struct{}

func (s *stubDraptoClient) Encode(ctx context.Context, inputPath, outputDir string, opts drapto.EncodeOptions) (string, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", err
	}
	if opts.Progress != nil {
		opts.Progress(drapto.ProgressUpdate{Stage: "Encoding", Percent: 10, Message: "starting"})
	}
	stem := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	if stem == "" {
		stem = filepath.Base(inputPath)
	}
	dest := filepath.Join(outputDir, stem+".mkv")
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", err
	}
	if opts.Progress != nil {
		opts.Progress(drapto.ProgressUpdate{Stage: "Encoding", Percent: 95, Message: "finishing"})
	}
	return dest, nil
}

type stubJellyfinService struct {
	root           string
	moviesDir      string
	tvDir          string
	organizeCalled bool
}

func (s *stubJellyfinService) Organize(ctx context.Context, sourcePath string, meta jellyfin.MediaMetadata) (string, error) {
	targetDir := meta.GetLibraryPath(s.root, s.moviesDir, s.tvDir)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}
	targetPath := filepath.Join(targetDir, meta.GetFilename()+filepath.Ext(sourcePath))
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(targetPath, data, 0o644); err != nil {
		return "", err
	}
	s.organizeCalled = true
	return targetPath, nil
}

func (s *stubJellyfinService) Refresh(ctx context.Context, meta jellyfin.MediaMetadata) error {
	return nil
}

type fakeTMDB struct {
	response *tmdb.Response
	queries  []string
}

func (f *fakeTMDB) SearchMovie(ctx context.Context, query string) (*tmdb.Response, error) {
	f.queries = append(f.queries, query)
	if f.response != nil {
		return f.response, nil
	}
	return &tmdb.Response{}, nil
}

func (f *fakeTMDB) SearchMovieWithOptions(ctx context.Context, query string, opts tmdb.SearchOptions) (*tmdb.Response, error) {
	return f.SearchMovie(ctx, query)
}

func (f *fakeTMDB) SearchTVWithOptions(ctx context.Context, query string, opts tmdb.SearchOptions) (*tmdb.Response, error) {
	return f.SearchMovie(ctx, query)
}

func (f *fakeTMDB) SearchMultiWithOptions(ctx context.Context, query string, opts tmdb.SearchOptions) (*tmdb.Response, error) {
	return f.SearchMovie(ctx, query)
}

func (f *fakeTMDB) GetSeasonDetails(ctx context.Context, showID int64, seasonNumber int) (*tmdb.SeasonDetails, error) {
	return &tmdb.SeasonDetails{}, nil
}

func (f *fakeTMDB) GetMovieDetails(ctx context.Context, movieID int64) (*tmdb.Result, error) {
	if f.response != nil && len(f.response.Results) > 0 {
		result := f.response.Results[0]
		result.MediaType = "movie"
		return &result, nil
	}
	return &tmdb.Result{ID: movieID, MediaType: "movie"}, nil
}

func (f *fakeTMDB) GetTVDetails(ctx context.Context, showID int64) (*tmdb.Result, error) {
	if f.response != nil && len(f.response.Results) > 0 {
		result := f.response.Results[0]
		result.MediaType = "tv"
		return &result, nil
	}
	return &tmdb.Result{ID: showID, MediaType: "tv"}, nil
}

type fakeDiscScanner struct {
	result     *disc.ScanResult
	calls      int
	lastDevice string
}

func (f *fakeDiscScanner) Scan(ctx context.Context, device string) (*disc.ScanResult, error) {
	f.calls++
	f.lastDevice = device
	if f.result == nil {
		return &disc.ScanResult{Fingerprint: "fallback", Titles: []disc.Title{}}, nil
	}
	return f.result, nil
}

type fakeMakemkvClient struct {
	calls int
}

func (f *fakeMakemkvClient) Rip(ctx context.Context, discTitle, destDir string, titleIDs []int, progress func(makemkv.ProgressUpdate)) (string, error) {
	f.calls++
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	filename := "integration-disc.mkv"
	target := filepath.Join(destDir, filename)
	data := bytes.Repeat([]byte{0xAF}, 20*1024*1024)
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", err
	}
	if progress != nil {
		progress(makemkv.ProgressUpdate{Stage: "Ripping", Percent: 25, Message: "starting"})
		progress(makemkv.ProgressUpdate{Stage: "Ripped", Percent: 100, Message: "done"})
	}
	return target, nil
}
