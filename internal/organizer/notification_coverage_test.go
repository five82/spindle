package organizer

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestTerminalNotificationDescribesReviewRoutingOutcomes(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	h := New(nil, nil, notify.New(server.URL, 5))
	sess := &stage.Session{Item: &queue.Item{ID: 5, DiscTitle: "Show", NeedsReview: 1}, Env: &ripspec.Envelope{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		library, review int
		phrase          string
	}{
		{2, 1, "Imported 2 items to the library; routed 1 item to review."},
		{0, 2, "Routed 2 items to review."},
		{0, 0, "Review is required before library import."},
	} {
		received = ""
		h.sendTerminalNotification(context.Background(), logger, sess, tc.library, tc.review)
		if !strings.Contains(received, tc.phrase) {
			t.Fatalf("library=%d review=%d: %q", tc.library, tc.review, received)
		}
	}
}
