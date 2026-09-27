package keydb

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloadRejectsIncompleteArchiveAndUnwritableTarget(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var payload []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	for _, tc := range []struct {
		payload           []byte
		destination, want string
	}{
		{[]byte("not zip"), dir, "open zip"},
		{zipWithEntry(t, "OTHER.cfg"), dir, "not found in zip"},
		{zipWithEntry(t, "dir/KEYDB.cfg"), filepath.Join(blocker, "child"), "create dir"},
	} {
		payload = tc.payload
		if err := Download(context.Background(), server.URL, tc.destination, time.Second, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("archive %q: %v", tc.want, err)
		}
	}
	if err := Download(context.Background(), "://invalid", dir, time.Second, nil); err == nil || !strings.Contains(err.Error(), "create request") {
		t.Fatalf("invalid URL: %v", err)
	}
}

func zipWithEntry(t *testing.T, name string) []byte {
	t.Helper()
	var b bytes.Buffer
	writer := zip.NewWriter(&b)
	file, err := writer.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("contents")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
