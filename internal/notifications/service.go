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

// Service defines the notification surface exposed to workflow components.
type Service interface {
	NotifyDiscDetected(ctx context.Context, discTitle, discType string) error
	NotifyIdentificationComplete(ctx context.Context, title, mediaType string) error
	NotifyRipStarted(ctx context.Context, discTitle string) error
	NotifyRipCompleted(ctx context.Context, discTitle string) error
	NotifyEncodingCompleted(ctx context.Context, discTitle string) error
	NotifyProcessingCompleted(ctx context.Context, title string) error
	NotifyOrganizationCompleted(ctx context.Context, mediaTitle, finalFile string) error
	NotifyQueueStarted(ctx context.Context, count int) error
	NotifyQueueCompleted(ctx context.Context, processed, failed int, duration time.Duration) error
	NotifyError(ctx context.Context, err error, context string) error
	NotifyUnidentifiedMedia(ctx context.Context, filename string) error
	TestNotification(ctx context.Context) error
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

func (n *ntfyService) NotifyDiscDetected(ctx context.Context, discTitle, discType string) error {
	discTitle = strings.TrimSpace(discTitle)
	discType = strings.TrimSpace(discType)
	if discType == "" {
		discType = "unknown"
	}
	data := payload{
		title:   "Spindle - Disc Detected",
		message: fmt.Sprintf("📀 Disc detected: %s (%s)", discTitle, discType),
		tags:    []string{"spindle", "disc", "detected"},
	}
	return n.send(ctx, data)
}

func (n *ntfyService) NotifyIdentificationComplete(ctx context.Context, title, mediaType string) error {
	title = strings.TrimSpace(title)
	mediaType = strings.TrimSpace(mediaType)
	if mediaType == "" {
		mediaType = "unknown"
	}
	data := payload{
		title:   "Spindle - Identified",
		message: fmt.Sprintf("🎬 Identified: %s (%s)", title, mediaType),
		tags:    []string{"spindle", "identify", "completed"},
	}
	return n.send(ctx, data)
}

func (n *ntfyService) NotifyRipStarted(ctx context.Context, discTitle string) error {
	discTitle = strings.TrimSpace(discTitle)
	data := payload{
		title:   "Spindle - Rip Started",
		message: fmt.Sprintf("Started ripping: %s", discTitle),
		tags:    []string{"spindle", "rip", "started"},
	}
	return n.send(ctx, data)
}

func (n *ntfyService) NotifyRipCompleted(ctx context.Context, discTitle string) error {
	discTitle = strings.TrimSpace(discTitle)
	data := payload{
		title:   "Spindle - Rip Complete",
		message: fmt.Sprintf("💿 Rip complete: %s", discTitle),
		tags:    []string{"spindle", "rip", "completed"},
	}
	return n.send(ctx, data)
}

func (n *ntfyService) NotifyEncodingCompleted(ctx context.Context, discTitle string) error {
	discTitle = strings.TrimSpace(discTitle)
	data := payload{
		title:   "Spindle - Encoded",
		message: fmt.Sprintf("🎞️ Encoding complete: %s", discTitle),
		tags:    []string{"spindle", "encode", "completed"},
	}
	return n.send(ctx, data)
}

func (n *ntfyService) NotifyProcessingCompleted(ctx context.Context, title string) error {
	title = strings.TrimSpace(title)
	data := payload{
		title:    "Spindle - Complete",
		message:  fmt.Sprintf("✅ Ready to watch: %s", title),
		tags:     []string{"spindle", "workflow", "completed"},
		priority: "high",
	}
	return n.send(ctx, data)
}

func (n *ntfyService) NotifyOrganizationCompleted(ctx context.Context, mediaTitle, finalFile string) error {
	mediaTitle = strings.TrimSpace(mediaTitle)
	finalFile = strings.TrimSpace(finalFile)
	message := fmt.Sprintf("Added to Plex: %s", mediaTitle)
	if finalFile != "" {
		message = fmt.Sprintf("%s\nFile: %s", message, finalFile)
	}
	data := payload{
		title:   "Spindle - Library Updated",
		message: message,
		tags:    []string{"spindle", "plex", "added"},
	}
	return n.send(ctx, data)
}

func (n *ntfyService) NotifyQueueStarted(ctx context.Context, count int) error {
	data := payload{
		title:   "Spindle - Queue Started",
		message: fmt.Sprintf("Started processing queue with %d items", count),
		tags:    []string{"spindle", "queue", "started"},
	}
	return n.send(ctx, data)
}

func (n *ntfyService) NotifyQueueCompleted(ctx context.Context, processed, failed int, duration time.Duration) error {
	duration = duration.Round(time.Second)
	if duration < 0 {
		duration = 0
	}
	durationText := duration.String()
	if duration == 0 {
		durationText = "0s"
	}

	var message string
	var title string
	if failed == 0 {
		title = "Spindle - Queue Complete"
		message = fmt.Sprintf("Queue processing complete: %d items processed in %s", processed, durationText)
	} else {
		title = "Spindle - Queue Complete (with errors)"
		message = fmt.Sprintf("Queue processing complete: %d succeeded, %d failed in %s", processed, failed, durationText)
	}

	data := payload{
		title:   title,
		message: message,
		tags:    []string{"spindle", "queue", "completed"},
	}
	return n.send(ctx, data)
}

func (n *ntfyService) NotifyError(ctx context.Context, err error, contextLabel string) error {
	var builder strings.Builder
	builder.WriteString("❌ Error")
	if contextLabel = strings.TrimSpace(contextLabel); contextLabel != "" {
		builder.WriteString(" with ")
		builder.WriteString(contextLabel)
	}
	builder.WriteString(": ")
	if err != nil {
		builder.WriteString(strings.TrimSpace(err.Error()))
	} else {
		builder.WriteString("unknown")
	}

	data := payload{
		title:    "Spindle - Error",
		message:  builder.String(),
		tags:     []string{"spindle", "error", "alert"},
		priority: "high",
	}
	return n.send(ctx, data)
}

func (n *ntfyService) NotifyUnidentifiedMedia(ctx context.Context, filename string) error {
	filename = strings.TrimSpace(filename)
	data := payload{
		title:   "Spindle - Unidentified Media",
		message: fmt.Sprintf("Could not identify: %s\nManual review required", filename),
		tags:    []string{"spindle", "unidentified", "review"},
	}
	return n.send(ctx, data)
}

func (n *ntfyService) TestNotification(ctx context.Context) error {
	data := payload{
		title:    "Spindle - Test",
		message:  "🧪 Notification system test",
		tags:     []string{"spindle", "test"},
		priority: "low",
	}
	return n.send(ctx, data)
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

func (noopService) NotifyDiscDetected(context.Context, string, string) error            { return nil }
func (noopService) NotifyIdentificationComplete(context.Context, string, string) error  { return nil }
func (noopService) NotifyRipStarted(context.Context, string) error                      { return nil }
func (noopService) NotifyRipCompleted(context.Context, string) error                    { return nil }
func (noopService) NotifyEncodingCompleted(context.Context, string) error               { return nil }
func (noopService) NotifyProcessingCompleted(context.Context, string) error             { return nil }
func (noopService) NotifyOrganizationCompleted(context.Context, string, string) error   { return nil }
func (noopService) NotifyQueueStarted(context.Context, int) error                       { return nil }
func (noopService) NotifyQueueCompleted(context.Context, int, int, time.Duration) error { return nil }
func (noopService) NotifyError(context.Context, error, string) error                    { return nil }
func (noopService) NotifyUnidentifiedMedia(context.Context, string) error               { return nil }
func (noopService) TestNotification(context.Context) error                              { return nil }
