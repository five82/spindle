package workflow_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"spindle/internal/disc"
	"spindle/internal/identification/tmdb"
	"spindle/internal/notifications"
	"spindle/internal/services/drapto"
	"spindle/internal/services/makemkv"
	"spindle/internal/services/plex"
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

type stubDraptoClient struct{}

func (s *stubDraptoClient) Encode(ctx context.Context, inputPath, outputDir string, progress func(drapto.ProgressUpdate)) (string, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", err
	}
	if progress != nil {
		progress(drapto.ProgressUpdate{Stage: "Encoding", Percent: 10, Message: "starting"})
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
	if progress != nil {
		progress(drapto.ProgressUpdate{Stage: "Encoding", Percent: 95, Message: "finishing"})
	}
	return dest, nil
}

type stubPlexService struct {
	root           string
	moviesDir      string
	tvDir          string
	organizeCalled bool
}

func (s *stubPlexService) Organize(ctx context.Context, sourcePath string, meta plex.MediaMetadata) (string, error) {
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

func (s *stubPlexService) Refresh(ctx context.Context, meta plex.MediaMetadata) error {
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

func (f *fakeMakemkvClient) Rip(ctx context.Context, discTitle, sourcePath, destDir string, progress func(makemkv.ProgressUpdate)) (string, error) {
	f.calls++
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	filename := "integration-disc.mkv"
	target := filepath.Join(destDir, filename)
	if err := os.WriteFile(target, []byte("ripped-data"), 0o644); err != nil {
		return "", err
	}
	if progress != nil {
		progress(makemkv.ProgressUpdate{Stage: "Ripping", Percent: 25, Message: "starting"})
		progress(makemkv.ProgressUpdate{Stage: "Ripped", Percent: 100, Message: "done"})
	}
	return target, nil
}
