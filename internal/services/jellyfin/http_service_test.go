package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"spindle/internal/config"
)

func TestHTTPServiceRefreshTriggersJellyfin(t *testing.T) {
	refreshCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Library/Refresh":
			if token := r.Header.Get("X-Emby-Token"); token != "token-123" {
				t.Fatalf("unexpected token: %q", token)
			}
			refreshCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Paths.LibraryDir = t.TempDir()
	cfg.Library.MoviesDir = "Movies"
	cfg.Library.TVDir = "TV"
	cfg.Jellyfin.Enabled = true
	cfg.Jellyfin.URL = server.URL
	cfg.Jellyfin.APIKey = "token-123"

	svc := NewConfiguredService(&cfg)
	hs, ok := svc.(*httpService)
	if !ok {
		t.Fatalf("expected httpService, got %T", svc)
	}

	hs.simple.MoveFunc = func(src, dst string) error { return nil }

	meta := testMetadata{title: "Example", movie: true, filename: "Example"}
	if err := hs.Refresh(context.Background(), meta); err != nil {
		t.Fatalf("refresh returned error: %v", err)
	}

	if !refreshCalled {
		t.Fatal("expected refresh endpoint to be called")
	}
}

type testMetadata struct {
	title    string
	movie    bool
	filename string
}

func (m testMetadata) GetLibraryPath(root, moviesDir, tvDir string) string {
	if m.movie {
		return filepath.Join(root, moviesDir)
	}
	return filepath.Join(root, tvDir)
}

func (m testMetadata) GetFilename() string { return m.filename }

func (m testMetadata) IsMovie() bool { return m.movie }

func (m testMetadata) Title() string { return m.title }
