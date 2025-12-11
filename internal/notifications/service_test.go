package notifications_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		expectPriority string
		expectTags     string
	}{
		{
			name:  "identification completed",
			event: notifications.EventIdentificationCompleted,
			payload: notifications.Payload{
				"title":        "Interstellar",
				"year":         "2014",
				"mediaType":    "movie",
				"displayTitle": "Interstellar (2014)",
			},
			expectTitle:   "Spindle - Identified",
			expectMessage: "🎬 Identified: Interstellar (2014)",
			expectTags:    "identify",
		},
		{
			name:  "disc detected",
			event: notifications.EventDiscDetected,
			payload: notifications.Payload{
				"discTitle": "Blade Runner",
				"discType":  "bluray",
			},
			expectTitle:   "Spindle - Processing Disc",
			expectMessage: "📀 Processing new disc: Blade Runner (bluray)",
		},
		{
			name:  "rip completed",
			event: notifications.EventRipCompleted,
			payload: notifications.Payload{
				"discTitle": "Jurassic Park",
			},
			expectTitle:   "Spindle - Rip Complete",
			expectMessage: "💿 Rip complete: Jurassic Park",
			expectTags:    "rip",
		},
		{
			name:  "encoding completed",
			event: notifications.EventEncodingCompleted,
			payload: notifications.Payload{
				"discTitle":   "The Matrix",
				"ratio":       50.0,
				"inputBytes":  int64(1000),
				"outputBytes": int64(500),
				"files":       1,
				"preset":      "default",
			},
			expectTitle:   "Spindle - Encoded",
			expectMessage: "🎞️ Encoding complete: The Matrix\nPreset: default\nOutput: 500 B of 1000 B (50.0%)",
			expectTags:    "encode",
		},
		{
			name:  "organization completed",
			event: notifications.EventOrganizationCompleted,
			payload: notifications.Payload{
				"mediaTitle":    "Arrival",
				"finalFile":     "Arrival (2016).mkv",
				"plexRefreshed": true,
			},
			expectTitle:   "Spindle - Library Updated",
			expectMessage: "Added to Plex: Arrival\nFile: Arrival (2016).mkv\nPlex refresh requested",
			expectTags:    "organize",
		},
		{
			name:  "error",
			event: notifications.EventError,
			payload: notifications.Payload{
				"context": "rip",
				"error":   "failed to read disc",
			},
			expectTitle:    "Spindle - Error",
			expectMessage:  "❌ Error with rip: failed to read disc",
			expectPriority: "high",
			expectTags:     "error",
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
			if strings.TrimSpace(captured.tags) != strings.TrimSpace(tc.expectTags) {
				t.Fatalf("expected tags %q, got %q", tc.expectTags, captured.tags)
			}
			if captured.priority != tc.expectPriority {
				t.Fatalf("expected priority %q, got %q", tc.expectPriority, captured.priority)
			}
		})
	}
}

func TestNtfyServiceIgnoresSuppressedEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected call for suppressed event: %s", r.URL.String())
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.NtfyTopic = server.URL

	svc := notifications.NewService(&cfg)
	suppressed := []notifications.Event{
		notifications.EventRipStarted,
		notifications.EventProcessingCompleted,
	}

	for _, event := range suppressed {
		if err := svc.Publish(context.Background(), event, notifications.Payload{"value": "ignored"}); err != nil {
			t.Fatalf("expected no error for suppressed event %s, got %v", event, err)
		}
	}
}

func TestQueueNotificationsRespectMinimumCount(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.NtfyTopic = server.URL
	cfg.NotifyQueueMinItems = 2

	svc := notifications.NewService(&cfg)
	// Should suppress because below threshold.
	if err := svc.Publish(context.Background(), notifications.EventQueueStarted, notifications.Payload{"count": 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should send because count meets threshold.
	if err := svc.Publish(context.Background(), notifications.EventQueueStarted, notifications.Payload{"count": 3}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 notification sent, got %d", calls)
	}
}
