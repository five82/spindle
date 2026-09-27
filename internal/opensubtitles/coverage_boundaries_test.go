package opensubtitles

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSearchAndDownloadRejectBadResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/subtitles" {
			_, _ = w.Write([]byte("not json"))
			return
		}
		if r.URL.Path == "/download" {
			_, _ = w.Write([]byte("not json"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	c := New(Params{APIKey: "key", BaseURL: server.URL}, nil)
	c.rateDelay = 0
	c.maxRetries = 0
	if _, err := c.Search(context.Background(), 1, 0, 0, nil); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("search decode: %v", err)
	}
	if _, err := c.Download(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("download decode: %v", err)
	}
	if err := c.DownloadToFile(context.Background(), 1, filepath.Join(t.TempDir(), "file.srt")); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("download negotiation: %v", err)
	}
	c.baseURL = server.URL + "/not-found"
	if _, err := c.Search(context.Background(), 1, 0, 0, nil); err == nil || !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("search status: %v", err)
	}
	var nilClient *Client
	if _, err := nilClient.Search(context.Background(), 1, 0, 0, nil); err == nil {
		t.Fatal("nil search")
	}
	if _, err := nilClient.Download(context.Background(), 1); err == nil {
		t.Fatal("nil download")
	}
}

func TestDownloadFileFailurePreservesDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ok" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("subtitle"))
	}))
	defer server.Close()
	c := New(Params{APIKey: "key", BaseURL: server.URL}, nil)
	c.maxRetries = 0
	dest := filepath.Join(t.TempDir(), "subtitle.srt")
	if err := os.WriteFile(dest, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{"://invalid", server.URL} {
		if err := c.downloadLinkToFileOnce(context.Background(), link, dest); err == nil {
			t.Fatalf("%s should fail", link)
		}
		data, err := os.ReadFile(dest)
		if err != nil || string(data) != "original" {
			t.Fatalf("destination changed: %q %v", data, err)
		}
	}
	if err := c.downloadLinkToFileOnce(context.Background(), server.URL+"/ok", filepath.Join(dest, "bad.srt")); err == nil || !strings.Contains(err.Error(), "create dir") {
		t.Fatalf("non-directory parent: %v", err)
	}
	if err := c.downloadLinkToFileOnce(context.Background(), server.URL+"/ok", dest); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "subtitle" {
		t.Fatalf("successful replacement: %q %v", data, err)
	}
}

func TestDownloadBodyFailureLeavesExistingSubtitleUntouched(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/truncated" {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("short"))
			return
		}
		_, _ = w.Write([]byte("complete"))
	}))
	defer server.Close()
	c := New(Params{APIKey: "key", BaseURL: server.URL}, nil)
	dir := t.TempDir()
	dest := filepath.Join(dir, "movie.srt")
	if err := os.WriteFile(dest, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.downloadLinkToFileOnce(context.Background(), server.URL+"/truncated", dest); err == nil || !strings.Contains(err.Error(), "write") {
		t.Fatalf("truncated response: %v", err)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "old" {
		t.Fatalf("original subtitle changed: %q %v", data, err)
	}
	if err := c.downloadLinkToFileOnce(context.Background(), server.URL, dir); err == nil || !strings.Contains(err.Error(), "rename") {
		t.Fatalf("directory destination: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary download leaked: %s", e.Name())
		}
	}
}

func TestRetryClassificationAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&statusError{429}, true}, {&statusError{503}, true}, {&statusError{404}, false},
		{io.ErrUnexpectedEOF, true}, {syscall.ECONNRESET, true}, {syscall.ECONNREFUSED, true},
		{context.DeadlineExceeded, true}, {errors.New("not transient"), false},
	} {
		if got := isRetryable(tc.err); got != tc.want {
			t.Errorf("retryable(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
	c := New(Params{APIKey: "key"}, nil)
	c.maxRetries = 2
	c.retryDelay = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	_, err := c.doWithRetry(ctx, func() ([]byte, error) { attempts++; cancel(); return nil, &statusError{429} })
	if err != context.Canceled || attempts != 1 {
		t.Fatalf("canceled retry: %v, attempts %d", err, attempts)
	}
	c.rateDelay = time.Hour
	c.lastCall = time.Now().Add(-2 * time.Hour)
	c.rateLimit()
	if time.Since(c.lastCall) > time.Second {
		t.Fatal("last call not updated")
	}
	c.rateDelay = 50 * time.Millisecond
	c.lastCall = time.Now()
	c.rateLimit() // delay active: future requests may not overtake the minimum interval
	if c.lastCall.IsZero() {
		t.Fatal("rate limiter lost last call")
	}
	fresh := New(Params{APIKey: "key"}, nil)
	fresh.rateLimit() // first call never sleeps
	if fresh.lastCall.IsZero() {
		t.Fatal("first request not recorded")
	}
}
