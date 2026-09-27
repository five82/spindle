package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/httpapi"
)

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}

func TestDiscControlIdempotencyAndDetectError(t *testing.T) {
	store := testStore(t)
	monitor := discmonitor.New("/dev/no-such-disc", store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httpapi.New(httpapi.Params{Store: store, DiscMonitor: monitor, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	request := func(path string, status int) string {
		t.Helper()
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	if got := request("/api/disc/pause", http.StatusOK); !containsAll(got, "\"paused\":true", "\"changed\":true") {
		t.Fatal(got)
	}
	if got := request("/api/disc/pause", http.StatusOK); !containsAll(got, "\"paused\":true", "\"changed\":false") {
		t.Fatal(got)
	}
	if got := request("/api/disc/detect", http.StatusOK); !containsAll(got, "\"handled\":false", "paused") {
		t.Fatal(got)
	}
	if got := request("/api/disc/resume", http.StatusOK); !containsAll(got, "\"resumed\":true", "\"changed\":true") {
		t.Fatal(got)
	}
	if got := request("/api/disc/resume", http.StatusOK); !containsAll(got, "\"changed\":false") {
		t.Fatal(got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	request("/api/disc/detect", http.StatusInternalServerError)
}

func TestListenTCPReportsInvalidAddressAndClosesListener(t *testing.T) {
	srv := httpapi.New(httpapi.Params{Store: testStore(t), Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})
	if err := srv.ListenTCP("not a tcp address"); err == nil {
		t.Fatal("invalid address accepted")
	}
	if err := srv.ListenTCP("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
