package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewEmptyTopic(t *testing.T) {
	n := New("", 10, nil)
	if n != nil {
		t.Fatal("expected nil notifier for empty topic")
	}
}

func TestNewDefaultTimeout(t *testing.T) {
	n := New("http://example.com/topic", 0, nil)
	if n == nil {
		t.Fatal("expected non-nil notifier")
	}
	if n.timeout != 10*1e9 {
		t.Fatalf("expected 10s default timeout, got %v", n.timeout)
	}
}

func TestNilNotifierSend(t *testing.T) {
	var n *Notifier
	err := n.Send(context.Background(), EventTest, "title", "msg")
	if err != nil {
		t.Fatalf("expected nil error from nil notifier, got %v", err)
	}
}

func TestPriority(t *testing.T) {
	tests := []struct {
		event Event
		want  string
	}{
		{EventDiscDetected, "default"},
		{EventIdentificationComplete, "default"},
		{EventRipComplete, "default"},
		{EventEncodeComplete, "default"},
		{EventValidationFailed, "high"},
		{EventPipelineComplete, "default"},
		{EventOrganizeComplete, "default"},
		{EventQueueStarted, "default"},
		{EventQueueCompleted, "default"},
		{EventError, "high"},
		{EventRipCacheHit, "low"},
		{EventRipStarted, "default"},
		{EventEncodeStarted, "default"},
		{EventUnidentifiedMedia, "default"},
		{EventTest, "low"},
	}
	for _, tt := range tests {
		got := priority(tt.event)
		if got != tt.want {
			t.Errorf("priority(%q) = %q, want %q", tt.event, got, tt.want)
		}
	}
}

func TestTags(t *testing.T) {
	tests := []struct {
		event Event
		want  string
	}{
		{EventDiscDetected, ""},
		{EventIdentificationComplete, "identify"},
		{EventRipComplete, "rip"},
		{EventEncodeComplete, "encode"},
		{EventValidationFailed, "validation,warning"},
		{EventPipelineComplete, ""},
		{EventOrganizeComplete, "organize"},
		{EventQueueStarted, "queue"},
		{EventQueueCompleted, "queue"},
		{EventError, "error"},
		{EventRipCacheHit, "rip,cache"},
		{EventRipStarted, "rip"},
		{EventEncodeStarted, "encode"},
		{EventUnidentifiedMedia, "review"},
		{EventTest, "test"},
	}
	for _, tt := range tests {
		got := tags(tt.event)
		if got != tt.want {
			t.Errorf("tags(%q) = %q, want %q", tt.event, got, tt.want)
		}
	}
}

func TestSendHTTP(t *testing.T) {
	var (
		gotTitle     string
		gotPriority  string
		gotTags      string
		gotUserAgent string
		gotBody      string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotTags = r.Header.Get("Tags")
		gotUserAgent = r.Header.Get("User-Agent")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL, 5, nil)
	err := n.Send(context.Background(), EventValidationFailed, "Validation Failed", "file.mkv failed checks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotTitle != "Validation Failed" {
		t.Errorf("title = %q, want %q", gotTitle, "Validation Failed")
	}
	if gotPriority != "high" {
		t.Errorf("priority = %q, want %q", gotPriority, "high")
	}
	if gotTags != "validation,warning" {
		t.Errorf("tags = %q, want %q", gotTags, "validation,warning")
	}
	if gotUserAgent != "Spindle-Go/0.1.0" {
		t.Errorf("user-agent = %q, want %q", gotUserAgent, "Spindle-Go/0.1.0")
	}
	if gotBody != "file.mkv failed checks" {
		t.Errorf("body = %q, want %q", gotBody, "file.mkv failed checks")
	}
}

func TestSendHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := New(srv.URL, 5, nil)
	err := n.Send(context.Background(), EventError, "Error", "something broke")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestSendNoTagsHeader(t *testing.T) {
	var gotTagsPresent bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, gotTagsPresent = r.Header["Tags"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL, 5, nil)
	err := n.Send(context.Background(), EventDiscDetected, "Disc", "detected")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotTagsPresent {
		t.Error("Tags header should not be set for disc_detected event")
	}
}
