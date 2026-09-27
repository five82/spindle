package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/config"
)

func TestEncodeWorkerIsHiddenAndRequiresFiles(t *testing.T) {
	worker := newEncodeWorkerCmd()
	if !worker.Hidden {
		t.Fatal("worker visible to operators")
	}
	if err := worker.RunE(worker, nil); err == nil || !strings.Contains(err.Error(), "requires --input and --output-dir") {
		t.Fatalf("worker without files: %v", err)
	}
}

func TestNotifyCommandAndDurationFormatting(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg = &config.Config{}
	cmd := newTestNotifyCmd()
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("missing topic: %v", err)
	}
	var received string
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get("Title") + ":" + r.Header.Get("Tags")
		statusCopy := status
		w.WriteHeader(statusCopy)
	}))
	defer server.Close()
	cfg.Notifications.NtfyTopic = server.URL
	got := captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "Test notification sent") || received != "Spindle Test:test_tube" {
		t.Fatalf("send: %q %q", got, received)
	}
	status = http.StatusServiceUnavailable
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "send notification") {
		t.Fatalf("send failure: %v", err)
	}
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{{time.Second*8 + time.Millisecond*300, "8.3s"}, {time.Second * 125, "2m05s"}} {
		if got := formatPhaseDuration(tc.d); got != tc.want {
			t.Errorf("duration %v = %q", tc.d, got)
		}
	}
}
