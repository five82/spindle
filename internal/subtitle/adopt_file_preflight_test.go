package subtitle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/transcription"
)

func TestAdoptForFilePreflightAndSearchFailures(t *testing.T) {
	emptyServer := newCandidateSearchServer(t, `{"data":[]}`)
	t.Cleanup(emptyServer.Close)
	empty := opensubtitles.New(opensubtitles.Params{APIKey: "key", BaseURL: emptyServer.URL}, discardLogger())
	h := New(&config.Config{}, nil, empty)
	base := AdoptFileRequest{VideoPath: "movie.mkv", WorkDir: t.TempDir(), TMDBID: 42}
	for _, tc := range []struct {
		name   string
		change func(*AdoptFileRequest)
		want   string
	}{
		{"missing video", func(r *AdoptFileRequest) { r.VideoPath = " " }, "missing video path"},
		{"missing workdir", func(r *AdoptFileRequest) { r.WorkDir = " " }, "missing video path"},
		{"blocked workdir", func(r *AdoptFileRequest) {
			r.WorkDir = filepath.Join(r.WorkDir, "block", "child")
			if err := os.WriteFile(filepath.Dir(r.WorkDir), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "create work dir"},
		{"no candidates", func(*AdoptFileRequest) {}, "no usable candidates"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.change(&r)
			if _, err := h.AdoptForFile(context.Background(), r); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
	server := newCandidateSearchServer(t, `{"data":[{"id":"1","attributes":{"language":"en","files":[{"file_id":77}]}}]}`)
	defer server.Close()
	h = New(&config.Config{}, nil, opensubtitles.New(opensubtitles.Params{APIKey: "key", BaseURL: server.URL}, discardLogger()))
	if _, err := h.AdoptForFile(context.Background(), base); err == nil || !strings.Contains(err.Error(), "transcriber not configured") {
		t.Fatalf("missing transcriber: %v", err)
	}
	r := base
	r.Transcript = &transcription.TranscribeResult{SRTPath: filepath.Join(t.TempDir(), "absent.srt")}
	if _, err := h.AdoptForFile(context.Background(), r); err == nil {
		t.Fatal("missing sync transcript accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.AdoptForFile(ctx, base); err == nil || !strings.Contains(err.Error(), "opensubtitles search") {
		t.Fatalf("search failure: %v", err)
	}
}
