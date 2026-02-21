package preflight

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"spindle/internal/config"
)

func TestCheckDirectoryAccess_OK(t *testing.T) {
	dir := t.TempDir()
	result := CheckDirectoryAccess("test", dir)
	if !result.Passed {
		t.Fatalf("expected pass for temp dir, got: %s", result.Detail)
	}
}

func TestCheckDirectoryAccess_NotExist(t *testing.T) {
	result := CheckDirectoryAccess("test", filepath.Join(t.TempDir(), "nope"))
	if result.Passed {
		t.Fatal("expected failure for missing dir")
	}
	if result.Detail == "" {
		t.Fatal("expected non-empty detail")
	}
}

func TestCheckDirectoryAccess_NotDir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := CheckDirectoryAccess("test", f)
	if result.Passed {
		t.Fatal("expected failure for file path")
	}
}

func TestCheckJellyfin_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "good-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	result := CheckJellyfin(context.Background(), srv.URL, "good-key")
	if !result.Passed {
		t.Fatalf("expected pass, got: %s", result.Detail)
	}
}

func TestCheckJellyfin_BadKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	result := CheckJellyfin(context.Background(), srv.URL, "bad-key")
	if result.Passed {
		t.Fatal("expected failure for bad key")
	}
}

func TestCheckJellyfin_MissingURL(t *testing.T) {
	result := CheckJellyfin(context.Background(), "", "key")
	if result.Passed {
		t.Fatal("expected failure for missing URL")
	}
}

func TestCheckJellyfin_MissingKey(t *testing.T) {
	result := CheckJellyfin(context.Background(), "http://localhost", "")
	if result.Passed {
		t.Fatal("expected failure for missing key")
	}
}

func TestRunAll_NilConfig(t *testing.T) {
	results := RunAll(context.Background(), nil)
	if results != nil {
		t.Fatal("expected nil results for nil config")
	}
}

func TestRunAll_MinimalConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Paths.StagingDir = t.TempDir()
	cfg.Paths.LibraryDir = t.TempDir()
	cfg.Jellyfin.Enabled = false
	cfg.Commentary.Enabled = false

	results := RunAll(context.Background(), &cfg)
	// Should have staging + library directory checks
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if !r.Passed {
			t.Errorf("check %q failed: %s", r.Name, r.Detail)
		}
	}
}

func TestRunAll_IncludesJellyfinWhenEnabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Paths.StagingDir = t.TempDir()
	cfg.Paths.LibraryDir = ""
	cfg.Jellyfin.Enabled = true
	cfg.Jellyfin.URL = srv.URL
	cfg.Jellyfin.APIKey = "test"
	cfg.Commentary.Enabled = false

	results := RunAll(context.Background(), &cfg)
	found := false
	for _, r := range results {
		if r.Name == "Jellyfin" {
			found = true
			if !r.Passed {
				t.Errorf("Jellyfin check failed: %s", r.Detail)
			}
		}
	}
	if !found {
		t.Fatal("expected Jellyfin check in results")
	}
}
