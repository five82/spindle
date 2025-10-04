package notifications

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"spindle/internal/config"
)

const userAgent = "Spindle-Go/0.1.0"

// Event identifies a notification type understood by the notifier implementation.
type Event string

const (
	EventDiscDetected            Event = "disc_detected"
	EventIdentificationCompleted Event = "identification_completed"
	EventRipStarted              Event = "rip_started"
	EventRipCompleted            Event = "rip_completed"
	EventEncodingCompleted       Event = "encoding_completed"
	EventProcessingCompleted     Event = "processing_completed"
	EventOrganizationCompleted   Event = "organization_completed"
	EventQueueStarted            Event = "queue_started"
	EventQueueCompleted          Event = "queue_completed"
	EventError                   Event = "error"
	EventUnidentifiedMedia       Event = "unidentified_media"
	EventTestNotification        Event = "test"
)

// Payload carries contextual fields associated with a notification event.
type Payload map[string]any

// Service defines the notification surface exposed to workflow components.
type Service interface {
	Publish(ctx context.Context, event Event, payload Payload) error
}

// NewService builds a notification service backed by ntfy when configured.
// When no ntfy topic is configured, a noop implementation is returned.
func NewService(cfg *config.Config) Service {
	topic := strings.TrimSpace(cfg.NtfyTopic)
	if topic == "" {
		return noopService{}
	}

	timeout := time.Duration(cfg.NtfyRequestTimeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	client := &http.Client{Timeout: timeout}
	return &ntfyService{
		endpoint: topic,
		client:   client,
	}
}

type payload struct {
	title    string
	message  string
	priority string
}

type ntfyService struct {
	endpoint string
	client   *http.Client
}

func (n *ntfyService) Publish(ctx context.Context, event Event, data Payload) error {
	switch event {
	case EventDiscDetected:
		discTitle := strings.TrimSpace(payloadString(data, "discTitle"))
		discType := strings.TrimSpace(payloadString(data, "discType"))
		if discType == "" {
			discType = "unknown"
		}
		return n.send(ctx, payload{
			title:   "Spindle - Disc Detected",
			message: fmt.Sprintf("📀 Disc detected: %s (%s)", discTitle, discType),
		})
	case EventIdentificationCompleted:
		title := strings.TrimSpace(payloadString(data, "title"))
		year := strings.TrimSpace(payloadString(data, "year"))
		if title == "" || year == "" {
			return nil
		}
		display := strings.TrimSpace(payloadString(data, "displayTitle"))
		if display == "" {
			display = fmt.Sprintf("%s (%s)", title, year)
		}
		return n.send(ctx, payload{
			title:   "Spindle - Identified",
			message: fmt.Sprintf("🎬 Identified: %s", display),
		})
	case EventRipCompleted:
		discTitle := strings.TrimSpace(payloadString(data, "discTitle"))
		return n.send(ctx, payload{
			title:   "Spindle - Rip Complete",
			message: fmt.Sprintf("💿 Rip complete: %s", discTitle),
		})
	case EventEncodingCompleted:
		discTitle := strings.TrimSpace(payloadString(data, "discTitle"))
		return n.send(ctx, payload{
			title:   "Spindle - Encoded",
			message: fmt.Sprintf("🎞️ Encoding complete: %s", discTitle),
		})
	case EventOrganizationCompleted:
		mediaTitle := strings.TrimSpace(payloadString(data, "mediaTitle"))
		finalFile := strings.TrimSpace(payloadString(data, "finalFile"))
		message := fmt.Sprintf("Added to Plex: %s", mediaTitle)
		if finalFile != "" {
			message = fmt.Sprintf("%s\nFile: %s", message, finalFile)
		}
		return n.send(ctx, payload{
			title:   "Spindle - Library Updated",
			message: message,
		})
	case EventError:
		contextLabel := strings.TrimSpace(payloadString(data, "context"))
		errVal := payloadError(data, "error")
		var builder strings.Builder
		builder.WriteString("❌ Error")
		if contextLabel != "" {
			builder.WriteString(" with ")
			builder.WriteString(contextLabel)
		}
		builder.WriteString(": ")
		if errVal != "" {
			builder.WriteString(errVal)
		} else {
			builder.WriteString("unknown")
		}
		return n.send(ctx, payload{
			title:    "Spindle - Error",
			message:  builder.String(),
			priority: "high",
		})
	case EventTestNotification:
		return n.send(ctx, payload{
			title:    "Spindle - Test",
			message:  "🧪 Notification system test",
			priority: "low",
		})
	case EventRipStarted,
		EventProcessingCompleted,
		EventQueueStarted,
		EventQueueCompleted,
		EventUnidentifiedMedia:
		return nil
	default:
		return fmt.Errorf("unsupported notification event: %s", event)
	}
}

func (n *ntfyService) send(ctx context.Context, data payload) error {
	if n == nil || n.client == nil {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.endpoint, strings.NewReader(data.message))
	if err != nil {
		return fmt.Errorf("build ntfy request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if data.title != "" {
		req.Header.Set("Title", data.title)
	}
	if data.priority != "" && data.priority != "default" {
		req.Header.Set("Priority", data.priority)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("send ntfy notification: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("ntfy returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

type noopService struct{}

func (noopService) Publish(context.Context, Event, Payload) error { return nil }

func payloadString(data Payload, key string) string {
	if data == nil {
		return ""
	}
	if value, ok := data[key]; ok && value != nil {
		switch typed := value.(type) {
		case string:
			return typed
		case fmt.Stringer:
			return typed.String()
		default:
			return fmt.Sprintf("%v", typed)
		}
	}
	return ""
}

func payloadError(data Payload, key string) string {
	if data == nil {
		return ""
	}
	if value, ok := data[key]; ok && value != nil {
		switch typed := value.(type) {
		case error:
			return strings.TrimSpace(typed.Error())
		case string:
			return strings.TrimSpace(typed)
		case fmt.Stringer:
			return strings.TrimSpace(typed.String())
		}
	}
	return ""
}
