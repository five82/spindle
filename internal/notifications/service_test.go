package notifications_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
		expectTags     string
		expectPriority string
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
			expectTags:    "spindle,identify,movie",
		},
		{
			name:  "disc detected",
			event: notifications.EventDiscDetected,
			payload: notifications.Payload{
				"discTitle": "Blade Runner",
				"discType":  "bluray",
			},
			expectTitle:   "Spindle - Disc Detected",
			expectMessage: "📀 Disc detected: Blade Runner (bluray)",
			expectTags:    "spindle,disc,detected",
		},
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
			name:  "encoding completed",
			event: notifications.EventEncodingCompleted,
			payload: notifications.Payload{
				"discTitle": "The Matrix",
			},
			expectTitle:   "Spindle - Encoded",
			expectMessage: "🎞️ Encoding complete: The Matrix",
			expectTags:    "spindle,encode,completed",
		},
		{
			name:  "organization completed",
			event: notifications.EventOrganizationCompleted,
			payload: notifications.Payload{
				"mediaTitle": "Arrival",
				"finalFile":  "Arrival (2016).mkv",
			},
			expectTitle:   "Spindle - Library Updated",
			expectMessage: "Added to Plex: Arrival\nFile: Arrival (2016).mkv",
			expectTags:    "spindle,plex,added",
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
			expectTags:     "spindle,error,alert",
			expectPriority: "high",
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
		notifications.EventQueueStarted,
		notifications.EventQueueCompleted,
		notifications.EventUnidentifiedMedia,
	}

	for _, event := range suppressed {
		if err := svc.Publish(context.Background(), event, notifications.Payload{"value": "ignored"}); err != nil {
			t.Fatalf("expected no error for suppressed event %s, got %v", event, err)
		}
	}
}
