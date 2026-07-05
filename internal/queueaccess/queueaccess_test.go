package queueaccess

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenHTTPDaemonUnavailable(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "missing.sock")
	_, err := OpenHTTP(socketPath, "")
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("OpenHTTP error = %v, want ErrDaemonUnavailable", err)
	}
}

func TestListReturnsAPIItemShape(t *testing.T) {
	access := &HTTPAccess{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/queue" {
			t.Fatalf("path = %s, want /api/queue", r.URL.Path)
		}
		body := `{"items":[{"id":7,"discTitle":"Disc","stage":"encoding","tasks":[{"type":"encoding","state":"running","progress":{"message":"Encoding","percent":42}}],"episodes":[{"key":"s01e01","season":1,"episode":1}],"episodeTotals":{"planned":1,"encoded":1}}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}

	items, err := access.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	item := items[0]
	if len(item.Tasks) != 1 || item.Tasks[0].Progress.Message != "Encoding" || item.Tasks[0].Progress.Percent != 42 {
		t.Fatalf("tasks = %+v, want API task progress", item.Tasks)
	}
	if len(item.Episodes) != 1 || item.Episodes[0].Key != "s01e01" {
		t.Fatalf("episodes = %+v, want API episodes preserved", item.Episodes)
	}
	if item.EpisodeTotals == nil || item.EpisodeTotals.Planned != 1 || item.EpisodeTotals.Encoded != 1 {
		t.Fatalf("episode totals = %+v, want API totals preserved", item.EpisodeTotals)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
