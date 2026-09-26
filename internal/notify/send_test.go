package notify

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendLoggedRecordsSuccessAndFailure(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&log, nil))
	if err := SendLogged(context.Background(), nil, logger, EventTest, "Title", "message"); err != nil || log.Len() != 0 {
		t.Fatalf("disabled: %v %q", err, log.String())
	}
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
	defer server.Close()
	notifier := New(server.URL, 1)
	if err := SendLogged(context.Background(), notifier, logger, EventTest, "Title", "message", "item_id", int64(3)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"notification_sent", "test_tube", "item_id", "Title"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("success missing %q: %s", want, log.String())
		}
	}
	status = http.StatusServiceUnavailable
	err := SendLogged(context.Background(), notifier, logger, EventError, "Failed", "message", "item_id", int64(3))
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("failure: %v", err)
	}
	for _, want := range []string{"notification_failed", "notification delivery failed", "item_id", "Failed"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("failure missing %q: %s", want, log.String())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SendLogged(ctx, notifier, logger, EventTest, "Cancelled", "message"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}
