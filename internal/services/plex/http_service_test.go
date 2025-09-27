package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spindle/internal/config"
)

func TestHTTPServiceRefreshTriggersPlex(t *testing.T) {
	sectionsCalled := false
	refreshCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/library/sections":
			sectionsCalled = true
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<MediaContainer><Directory key="1" title="Movies"/><Directory key="2" title="TV Shows"/></MediaContainer>`))
		case r.URL.Path == "/library/sections/1/refresh":
			refreshCalled = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.LibraryDir = t.TempDir()
	cfg.LogDir = t.TempDir()
	cfg.MoviesDir = "Movies"
	cfg.TVDir = "TV"
	cfg.MoviesLibrary = "Movies"
	cfg.TVLibrary = "TV Shows"
	cfg.PlexURL = server.URL
	cfg.PlexLinkEnabled = true

	statePath := filepath.Join(cfg.LogDir, stateFileName)
	state := map[string]any{
		"client_identifier":   "test-client",
		"authorization_token": "auth-token",
		"token":               "token",
		"token_expires_at":    time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	service := NewConfiguredService(&cfg)
	hs, ok := service.(*httpService)
	if !ok {
		t.Fatalf("expected httpService, got %T", service)
	}

	hs.simple.MoveFunc = func(src, dst string) error { return nil }

	meta := testMetadata{title: "Example", movie: true, filename: "Example"}
	if err := hs.Refresh(context.Background(), meta); err != nil {
		t.Fatalf("refresh returned error: %v", err)
	}

	if !sectionsCalled {
		t.Fatal("expected sections endpoint to be called")
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
