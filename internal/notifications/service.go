package notifications

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
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
	host, _ := os.Hostname()
	return &ntfyService{
		endpoint: topic,
		client:   client,
		cfg:      buildNotifyConfig(cfg),
		host:     strings.TrimSpace(host),
		lastSent: make(map[string]time.Time),
	}
}

type payload struct {
	title    string
	message  string
	priority string
	tags     []string
}

type ntfyService struct {
	endpoint string
	client   *http.Client
	cfg      notifyConfig
	host     string

	mu       sync.Mutex
	lastSent map[string]time.Time
}

type notifyConfig struct {
	notifyIdentification bool
	notifyRip            bool
	notifyEncoding       bool
	notifyOrganization   bool
	notifyQueue          bool
	notifyReview         bool
	notifyErrors         bool
	minRipSeconds        int
	queueMinItems        int
	dedupeWindow         time.Duration
}

func (n *ntfyService) Publish(ctx context.Context, event Event, data Payload) error {
	if n == nil || n.client == nil {
		return nil
	}

	switch event {
	case EventDiscDetected:
		if !n.cfg.notifyIdentification {
			return nil
		}
		discTitle := strings.TrimSpace(payloadString(data, "discTitle"))
		discType := strings.TrimSpace(payloadString(data, "discType"))
		if discType == "" {
			discType = "unknown"
		}
		return n.sendOnce(ctx, event, data, payload{
			title:   "Spindle - Processing Disc",
			message: fmt.Sprintf("📀 Processing new disc: %s (%s)", discTitle, discType),
		})
	case EventIdentificationCompleted:
		if !n.cfg.notifyIdentification {
			return nil
		}
		title := strings.TrimSpace(payloadString(data, "title"))
		year := strings.TrimSpace(payloadString(data, "year"))
		display := strings.TrimSpace(payloadString(data, "displayTitle"))
		if display == "" && title != "" && year != "" {
			display = fmt.Sprintf("%s (%s)", title, year)
		}
		if display == "" {
			return nil
		}
		return n.sendOnce(ctx, event, data, payload{
			title:   "Spindle - Identified",
			message: fmt.Sprintf("🎬 Identified: %s", display),
			tags:    []string{"identify"},
		})
	case EventRipCompleted:
		if !n.cfg.notifyRip {
			return nil
		}
		discTitle := strings.TrimSpace(payloadString(data, "discTitle"))
		cache := strings.TrimSpace(payloadString(data, "cache"))
		duration := payloadDuration(data, "duration")
		bytes := payloadInt64(data, "bytes")
		// Suppress quick cache hits unless explicitly long.
		if cache == "hit" && duration < time.Duration(n.cfg.minRipSeconds)*time.Second {
			return nil
		}
		lines := []string{}
		if duration > 0 {
			lines = append(lines, fmt.Sprintf("Duration: %s", duration.Truncate(time.Second)))
		}
		if bytes > 0 {
			lines = append(lines, fmt.Sprintf("Size: %s", humanBytes(bytes)))
		}
		if cache != "" {
			lines = append(lines, fmt.Sprintf("Cache: %s", cache))
		}
		message := fmt.Sprintf("💿 Rip complete: %s", discTitle)
		if len(lines) > 0 {
			message = fmt.Sprintf("%s\n%s", message, strings.Join(lines, "\n"))
		}
		return n.sendOnce(ctx, event, data, payload{
			title:   "Spindle - Rip Complete",
			message: message,
			tags:    []string{"rip"},
		})
	case EventEncodingCompleted:
		if !n.cfg.notifyEncoding {
			return nil
		}
		discTitle := strings.TrimSpace(payloadString(data, "discTitle"))
		placeholder := payloadBool(data, "placeholder")
		if placeholder {
			return nil
		}
		ratio := payloadFloat(data, "ratio")
		output := payloadInt64(data, "outputBytes")
		input := payloadInt64(data, "inputBytes")
		files := payloadInt(data, "files")
		preset := strings.TrimSpace(payloadString(data, "preset"))

		lines := []string{}
		if preset != "" {
			lines = append(lines, fmt.Sprintf("Preset: %s", preset))
		}
		if input > 0 && output > 0 {
			lines = append(lines, fmt.Sprintf("Output: %s of %s (%.1f%%)", humanBytes(output), humanBytes(input), ratio))
		} else if output > 0 {
			lines = append(lines, fmt.Sprintf("Output: %s", humanBytes(output)))
		}
		if files > 1 {
			lines = append(lines, fmt.Sprintf("Files: %d", files))
		}
		message := fmt.Sprintf("🎞️ Encoding complete: %s", discTitle)
		if len(lines) > 0 {
			message = fmt.Sprintf("%s\n%s", message, strings.Join(lines, "\n"))
		}
		return n.sendOnce(ctx, event, data, payload{
			title:   "Spindle - Encoded",
			message: message,
			tags:    []string{"encode"},
		})
	case EventOrganizationCompleted:
		if !n.cfg.notifyOrganization {
			return nil
		}
		mediaTitle := strings.TrimSpace(payloadString(data, "mediaTitle"))
		finalFile := strings.TrimSpace(payloadString(data, "finalFile"))
		plexRefreshed := payloadBool(data, "plexRefreshed")
		message := fmt.Sprintf("Added to Plex: %s", mediaTitle)
		if finalFile != "" {
			message = fmt.Sprintf("%s\nFile: %s", message, finalFile)
		}
		if plexRefreshed {
			message = fmt.Sprintf("%s\nPlex refresh requested", message)
		}
		return n.sendOnce(ctx, event, data, payload{
			title:   "Spindle - Library Updated",
			message: message,
			tags:    []string{"organize"},
		})
	case EventError:
		if !n.cfg.notifyErrors {
			return nil
		}
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
		return n.sendOnce(ctx, event, data, payload{
			title:    "Spindle - Error",
			message:  builder.String(),
			priority: "high",
			tags:     []string{"error"},
		})
	case EventTestNotification:
		return n.sendOnce(ctx, event, data, payload{
			title:    "Spindle - Test",
			message:  "🧪 Notification system test",
			priority: "low",
			tags:     []string{"test"},
		})
	case EventRipStarted,
		EventProcessingCompleted:
		return nil
	case EventQueueStarted:
		if !n.cfg.notifyQueue {
			return nil
		}
		count := payloadInt(data, "count")
		if count < n.cfg.queueMinItems {
			return nil
		}
		return n.sendOnce(ctx, event, data, payload{
			title:   "Spindle - Queue Started",
			message: fmt.Sprintf("Items: %d\nHost: %s", count, n.host),
			tags:    []string{"queue"},
		})
	case EventQueueCompleted:
		if !n.cfg.notifyQueue {
			return nil
		}
		processed := payloadInt(data, "processed")
		failed := payloadInt(data, "failed")
		duration := payloadDuration(data, "duration")
		if processed+failed < n.cfg.queueMinItems {
			return nil
		}
		lines := []string{
			fmt.Sprintf("Processed: %d", processed),
			fmt.Sprintf("Failed: %d", failed),
		}
		if duration > 0 {
			lines = append(lines, fmt.Sprintf("Elapsed: %s", duration.Truncate(time.Second)))
		}
		return n.sendOnce(ctx, event, data, payload{
			title:   "Spindle - Queue Completed",
			message: strings.Join(lines, "\n"),
			tags:    []string{"queue"},
		})
	case EventUnidentifiedMedia:
		if !n.cfg.notifyReview {
			return nil
		}
		filename := strings.TrimSpace(payloadString(data, "filename"))
		reason := strings.TrimSpace(payloadString(data, "reason"))
		msg := fmt.Sprintf("Needs review: %s", filename)
		if reason != "" {
			msg = fmt.Sprintf("%s\nReason: %s", msg, reason)
		}
		return n.sendOnce(ctx, event, data, payload{
			title:   "Spindle - Review Needed",
			message: msg,
			tags:    []string{"review"},
		})
	default:
		return fmt.Errorf("unsupported notification event: %s", event)
	}
}

func (n *ntfyService) send(ctx context.Context, data payload) error {
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
	if len(data.tags) > 0 {
		req.Header.Set("Tags", strings.Join(data.tags, ","))
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

func (n *ntfyService) sendOnce(ctx context.Context, event Event, data Payload, built payload) error {
	if n.isDuplicate(event, data) {
		return nil
	}
	return n.send(ctx, built)
}

func buildNotifyConfig(cfg *config.Config) notifyConfig {
	if cfg == nil {
		return notifyConfig{
			notifyIdentification: true,
			notifyRip:            true,
			notifyEncoding:       true,
			notifyOrganization:   true,
			notifyQueue:          true,
			notifyReview:         true,
			notifyErrors:         true,
			minRipSeconds:        120,
			queueMinItems:        2,
			dedupeWindow:         10 * time.Minute,
		}
	}
	window := time.Duration(cfg.NotifyDedupWindowSeconds) * time.Second
	if window < 0 {
		window = 0
	}
	return notifyConfig{
		notifyIdentification: cfg.NotifyIdentification,
		notifyRip:            cfg.NotifyRip,
		notifyEncoding:       cfg.NotifyEncoding,
		notifyOrganization:   cfg.NotifyOrganization,
		notifyQueue:          cfg.NotifyQueue,
		notifyReview:         cfg.NotifyReview,
		notifyErrors:         cfg.NotifyErrors,
		minRipSeconds:        cfg.NotifyMinRipSeconds,
		queueMinItems:        cfg.NotifyQueueMinItems,
		dedupeWindow:         window,
	}
}

func (n *ntfyService) isDuplicate(event Event, data Payload) bool {
	if n.cfg.dedupeWindow <= 0 {
		return false
	}
	key := dedupeKey(event, data)
	if key == "" {
		return false
	}
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if prev, ok := n.lastSent[key]; ok && now.Sub(prev) < n.cfg.dedupeWindow {
		return true
	}
	n.lastSent[key] = now
	return false
}

func dedupeKey(event Event, data Payload) string {
	labelFields := []string{"discTitle", "mediaTitle", "title", "filename", "context"}
	parts := []string{string(event)}
	for _, field := range labelFields {
		if val := strings.TrimSpace(payloadString(data, field)); val != "" {
			parts = append(parts, val)
			break
		}
	}
	switch event {
	case EventQueueStarted:
		if count := payloadInt(data, "count"); count > 0 {
			parts = append(parts, fmt.Sprintf("count=%d", count))
		}
	case EventQueueCompleted:
		processed := payloadInt(data, "processed")
		failed := payloadInt(data, "failed")
		if processed > 0 || failed > 0 {
			parts = append(parts, fmt.Sprintf("p=%d,f=%d", processed, failed))
		}
	}
	return strings.Join(parts, "|")
}

func humanBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	const unit = 1024.0
	size := float64(value)
	for _, suffix := range []string{"KiB", "MiB", "GiB", "TiB"} {
		size /= unit
		if size < unit {
			return fmt.Sprintf("%.1f %s", size, suffix)
		}
	}
	return fmt.Sprintf("%.1f PiB", size/unit)
}

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
		}
	}
	return 0
}

func payloadInt64(data Payload, key string) int64 {
	if data == nil {
		return 0
	}
	if value, ok := data[key]; ok && value != nil {
		switch typed := value.(type) {
		case int64:
			return typed
		case int:
			return int64(typed)
		case float64:
			return int64(typed)
		}
	}
	return 0
}

func payloadInt(data Payload, key string) int {
	if data == nil {
		return 0
	}
	if value, ok := data[key]; ok && value != nil {
		switch typed := value.(type) {
		case int:
			return typed
		case int64:
			return int(typed)
		case float64:
			return int(typed)
		}
	}
	return 0
}

func payloadBool(data Payload, key string) bool {
	if data == nil {
		return false
	}
	if value, ok := data[key]; ok && value != nil {
		switch typed := value.(type) {
		case bool:
			return typed
		case string:
			return strings.EqualFold(strings.TrimSpace(typed), "true")
		}
	}
	return false
}

func payloadFloat(data Payload, key string) float64 {
	if data == nil {
		return 0
	}
	if value, ok := data[key]; ok && value != nil {
		switch typed := value.(type) {
		case float64:
			return typed
		case float32:
			return float64(typed)
		case int:
			return float64(typed)
		case int64:
			return float64(typed)
		}
	}
	return 0
}
