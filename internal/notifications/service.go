package notifications

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
	tags     []string
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
			tags:    []string{"spindle", "disc", "detected"},
		})
	case EventIdentificationCompleted:
		title := strings.TrimSpace(payloadString(data, "title"))
		mediaType := strings.TrimSpace(payloadString(data, "mediaType"))
		if mediaType == "" {
			mediaType = "unknown"
		}
		return n.send(ctx, payload{
			title:   "Spindle - Identified",
			message: fmt.Sprintf("🎬 Identified: %s (%s)", title, mediaType),
			tags:    []string{"spindle", "identify", "completed"},
		})
	case EventRipStarted:
		discTitle := strings.TrimSpace(payloadString(data, "discTitle"))
		return n.send(ctx, payload{
			title:   "Spindle - Rip Started",
			message: fmt.Sprintf("Started ripping: %s", discTitle),
			tags:    []string{"spindle", "rip", "started"},
		})
	case EventRipCompleted:
		discTitle := strings.TrimSpace(payloadString(data, "discTitle"))
		return n.send(ctx, payload{
			title:   "Spindle - Rip Complete",
			message: fmt.Sprintf("💿 Rip complete: %s", discTitle),
			tags:    []string{"spindle", "rip", "completed"},
		})
	case EventEncodingCompleted:
		discTitle := strings.TrimSpace(payloadString(data, "discTitle"))
		return n.send(ctx, payload{
			title:   "Spindle - Encoded",
			message: fmt.Sprintf("🎞️ Encoding complete: %s", discTitle),
			tags:    []string{"spindle", "encode", "completed"},
		})
	case EventProcessingCompleted:
		title := strings.TrimSpace(payloadString(data, "title"))
		return n.send(ctx, payload{
			title:    "Spindle - Complete",
			message:  fmt.Sprintf("✅ Ready to watch: %s", title),
			tags:     []string{"spindle", "workflow", "completed"},
			priority: "high",
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
			tags:    []string{"spindle", "plex", "added"},
		})
	case EventQueueStarted:
		count := payloadInt(data, "count")
		return n.send(ctx, payload{
			title:   "Spindle - Queue Started",
			message: fmt.Sprintf("Started processing queue with %d items", count),
			tags:    []string{"spindle", "queue", "started"},
		})
	case EventQueueCompleted:
		processed := payloadInt(data, "processed")
		failed := payloadInt(data, "failed")
		duration := payloadDuration(data, "duration").Round(time.Second)
		if duration < 0 {
			duration = 0
		}
		durationText := duration.String()
		if duration == 0 {
			durationText = "0s"
		}

		var title string
		var message string
		if failed == 0 {
			title = "Spindle - Queue Complete"
			message = fmt.Sprintf("Queue processing complete: %d items processed in %s", processed, durationText)
		} else {
			title = "Spindle - Queue Complete (with errors)"
			message = fmt.Sprintf("Queue processing complete: %d succeeded, %d failed in %s", processed, failed, durationText)
		}
		return n.send(ctx, payload{
			title:   title,
			message: message,
			tags:    []string{"spindle", "queue", "completed"},
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
			tags:     []string{"spindle", "error", "alert"},
			priority: "high",
		})
	case EventUnidentifiedMedia:
		filename := strings.TrimSpace(payloadString(data, "filename"))
		if filename == "" {
			filename = strings.TrimSpace(payloadString(data, "label"))
		}
		return n.send(ctx, payload{
			title:   "Spindle - Unidentified Media",
			message: fmt.Sprintf("Could not identify: %s\nManual review required", filename),
			tags:    []string{"spindle", "unidentified", "review"},
		})
	case EventTestNotification:
		return n.send(ctx, payload{
			title:    "Spindle - Test",
			message:  "🧪 Notification system test",
			tags:     []string{"spindle", "test"},
			priority: "low",
		})
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
	if len(data.tags) > 0 {
		req.Header.Set("Tags", strings.Join(data.tags, ","))
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

func payloadInt(data Payload, key string) int {
	if data == nil {
		return 0
	}
	if value, ok := data[key]; ok && value != nil {
		switch typed := value.(type) {
		case int:
			return typed
		case int32:
			return int(typed)
		case int64:
			return int(typed)
		case uint:
			return int(typed)
		case uint32:
			return int(typed)
		case uint64:
			return int(typed)
		case fmt.Stringer:
			if parsed, err := strconv.Atoi(typed.String()); err == nil {
				return parsed
			}
		case string:
			if parsed, err := strconv.Atoi(typed); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func payloadDuration(data Payload, key string) time.Duration {
	if data == nil {
		return 0
	}
	if value, ok := data[key]; ok && value != nil {
		switch typed := value.(type) {
		case time.Duration:
			return typed
		case int64:
			return time.Duration(typed)
		case int:
			return time.Duration(typed)
		case float64:
			return time.Duration(typed)
		case string:
			if parsed, err := time.ParseDuration(typed); err == nil {
				return parsed
			}
		}
	}
	return 0
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
