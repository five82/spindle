package queueaccess

import (
	"errors"
	"net/http"
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func TestEveryQueueMutationAndReadReportsUnavailableDaemon(t *testing.T) {
	a := &HTTPAccess{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("socket closed") })}}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"logs", func() error { _, _, err := a.Logs(LogsQuery{Tail: true}); return err }},
		{"list", func() error { _, err := a.List(queue.StageFailed); return err }},
		{"item", func() error { _, err := a.GetByID(7); return err }},
		{"status", func() error { _, err := a.Status(); return err }},
		{"retry", func() error { _, err := a.Retry(7); return err }},
		{"episode", func() error { _, err := a.RetryEpisode(7, "s01e01"); return err }},
		{"stop", func() error { _, err := a.Stop(7); return err }},
		{"enqueue", func() error { _, err := a.EnqueueCached(EnqueueCachedRequest{DiscTitle: "Movie"}); return err }},
		{"clear", func() error { _, err := a.Clear("completed"); return err }},
		{"remove", func() error { _, err := a.Remove(7); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrDaemonUnavailable) {
				t.Fatalf("disconnected daemon: %v", err)
			}
		})
	}
}
