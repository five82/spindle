package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewEmptyTopic(t *testing.T) {
	for _, topic := range []string{"", "  \t"} {
		if n := New(topic, 10); n != nil {
			t.Fatalf("New(%q) = non-nil, want nil", topic)
		}
	}
}

func TestNewTrimsTopicAndDefaultsTimeout(t *testing.T) {
	n := New("  http://example.com/topic  ", 0)
	if n == nil {
		t.Fatal("expected non-nil notifier")
	}
	if n.topic != "http://example.com/topic" {
		t.Fatalf("topic = %q", n.topic)
	}
	if n.timeout != 10*time.Second {
		t.Fatalf("expected 10s default timeout, got %v", n.timeout)
	}
}

func TestNilNotifierSend(t *testing.T) {
	var n *Notifier
	if err := n.Send(context.Background(), EventTest, "title", "msg"); err != nil {
		t.Fatalf("expected nil error from nil notifier, got %v", err)
	}
}

func TestEventPresentation(t *testing.T) {
	tests := []struct {
		event    Event
		priority string
		tag      string
	}{
		{EventItemQueued, "low", "inbox_tray"},
		{EventIdentificationComplete, "low", "mag"},
		{EventRipCacheHit, "low", "cd"},
		{EventRipComplete, "default", "cd"},
		{EventReviewRequired, "default", "warning"},
		{EventPipelineComplete, "default", "heavy_check_mark"},
		{EventError, "high", "rotating_light"},
		{EventTest, "low", "test_tube"},
	}
	for _, tt := range tests {
		if got := priority(tt.event); got != tt.priority {
			t.Errorf("priority(%q) = %q, want %q", tt.event, got, tt.priority)
		}
		if got := tags(tt.event); got != tt.tag {
			t.Errorf("tags(%q) = %q, want %q", tt.event, got, tt.tag)
		}
	}
}

func TestSendHTTP(t *testing.T) {
	var (
		gotTitle       string
		gotPriority    string
		gotTags        string
		gotUserAgent   string
		gotContentType string
		gotBody        string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotTags = r.Header.Get("Tags")
		gotUserAgent = r.Header.Get("User-Agent")
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL, 5)
	err := n.Send(context.Background(), EventReviewRequired, "Review needed: Amélie", "file.mkv needs review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotTitle != "Review needed: Amélie" {
		t.Errorf("title = %q", gotTitle)
	}
	if gotPriority != "default" {
		t.Errorf("priority = %q, want default", gotPriority)
	}
	if gotTags != "warning" {
		t.Errorf("tags = %q, want warning", gotTags)
	}
	if gotUserAgent != "Spindle" {
		t.Errorf("user-agent = %q, want Spindle", gotUserAgent)
	}
	if gotContentType != "text/plain; charset=utf-8" {
		t.Errorf("content-type = %q", gotContentType)
	}
	if gotBody != "file.mkv needs review" {
		t.Errorf("body = %q", gotBody)
	}
}

func TestSendRejectsNon2xxWithBoundedDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMultipleChoices)
		_, _ = io.WriteString(w, "bad topic")
	}))
	defer srv.Close()

	err := New(srv.URL, 5).Send(context.Background(), EventError, "Error", "something broke")
	if err == nil || !strings.Contains(err.Error(), "status 300: bad topic") {
		t.Fatalf("error = %v", err)
	}
}

func TestSendErrorRedactsTopicURL(t *testing.T) {
	const secret = "super-secret-topic"
	err := New("://"+secret, 5).Send(context.Background(), EventError, "Error", "message")
	if err == nil {
		t.Fatal("expected malformed URL error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked topic: %v", err)
	}
}

func TestSendNoTagsHeader(t *testing.T) {
	var gotTagsPresent bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, gotTagsPresent = r.Header["Tags"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := New(srv.URL, 5).Send(context.Background(), Event("unknown"), "Disc", "detected"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotTagsPresent {
		t.Error("Tags header should not be set for unknown event")
	}
}
