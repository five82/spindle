package notifications_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"spindle/internal/config"
	"spindle/internal/notifications"
)

func TestNewServiceReturnsNoopWhenTopicMissing(t *testing.T) {
	cfg := config.Default()
	cfg.NtfyTopic = ""
	svc := notifications.NewService(&cfg)
	if err := svc.Publish(context.Background(), notifications.EventRipCompleted, notifications.Payload{"discTitle": "Example"}); err != nil {
		t.Fatalf("expected noop notifier to return nil, got %v", err)
	}
}

func TestNtfyServiceFormatsPayloads(t *testing.T) {
	tests := []struct {
		name           string
		event          notifications.Event
		payload        notifications.Payload
		expectTitle    string
		expectMessage  string
		expectTags     string
		expectPriority string
	}{
		{
			name:  "rip completed",
			event: notifications.EventRipCompleted,
			payload: notifications.Payload{
				"discTitle": "Jurassic Park",
			},
			expectTitle:   "Spindle - Rip Complete",
			expectMessage: "💿 Rip complete: Jurassic Park",
			expectTags:    "spindle,rip,completed",
		},
		{
			name:  "processing complete",
			event: notifications.EventProcessingCompleted,
			payload: notifications.Payload{
				"title": "The Matrix",
			},
			expectTitle:    "Spindle - Complete",
			expectMessage:  "✅ Ready to watch: The Matrix",
			expectTags:     "spindle,workflow,completed",
			expectPriority: "high",
		},
		{
			name:  "queue completed with failures",
			event: notifications.EventQueueCompleted,
			payload: notifications.Payload{
				"processed": 3,
				"failed":    1,
				"duration":  5*time.Minute + 3*time.Second,
			},
			expectTitle:   "Spindle - Queue Complete (with errors)",
			expectMessage: "Queue processing complete: 3 succeeded, 1 failed in 5m3s",
			expectTags:    "spindle,queue,completed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured struct {
				title    string
				tags     string
				priority string
				body     string
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Fatalf("unexpected method: %s", r.Method)
				}
				captured.title = r.Header.Get("Title")
				captured.tags = r.Header.Get("Tags")
				captured.priority = r.Header.Get("Priority")
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read body: %v", err)
				}
				captured.body = string(body)
				_ = r.Body.Close()
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			cfg := config.Default()
			cfg.NtfyTopic = server.URL
			cfg.NtfyRequestTimeout = 5

			svc := notifications.NewService(&cfg)
			if err := svc.Publish(context.Background(), tc.event, tc.payload); err != nil {
				t.Fatalf("notification returned error: %v", err)
			}

			if captured.title != tc.expectTitle {
				t.Fatalf("expected title %q, got %q", tc.expectTitle, captured.title)
			}
			if captured.body != tc.expectMessage {
				t.Fatalf("expected message %q, got %q", tc.expectMessage, captured.body)
			}
			if captured.tags != tc.expectTags {
				t.Fatalf("expected tags %q, got %q", tc.expectTags, captured.tags)
			}
			if captured.priority != tc.expectPriority {
				t.Fatalf("expected priority %q, got %q", tc.expectPriority, captured.priority)
			}
		})
	}
}
