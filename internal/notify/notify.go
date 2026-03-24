package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Event represents a notification event type.
type Event string

const (
	EventDiscDetected           Event = "disc_detected"
	EventIdentificationComplete Event = "identification_complete"
	EventRipComplete            Event = "rip_complete"
	EventEncodeComplete         Event = "encode_complete"
	EventValidationFailed       Event = "validation_failed"
	EventPipelineComplete       Event = "pipeline_complete"
	EventOrganizeComplete       Event = "organize_complete"
	EventQueueStarted           Event = "queue_started"
	EventQueueCompleted         Event = "queue_completed"
	EventError                  Event = "error"
	EventUnidentifiedMedia      Event = "unidentified_media"
	EventTest                   Event = "test"
)

// Notifier sends notifications via ntfy.
type Notifier struct {
	topic   string
	timeout time.Duration
	client  *http.Client
	logger  *slog.Logger
}

// New creates a Notifier. Returns nil if topic is empty (notifications disabled).
func New(topic string, timeoutSeconds int, logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	if topic == "" {
		return nil
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Notifier{
		topic:   topic,
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
		logger:  logger,
	}
}

// Send sends a notification. Returns nil if Notifier is nil (disabled).
func (n *Notifier) Send(ctx context.Context, event Event, title, message string) error {
	if n == nil {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.topic, strings.NewReader(message))
	if err != nil {
		return fmt.Errorf("notify: create request: %w", err)
	}

	req.Header.Set("Title", title)
	req.Header.Set("Priority", priority(event))
	if t := tags(event); t != "" {
		req.Header.Set("Tags", t)
	}
	req.Header.Set("User-Agent", "Spindle-Go/0.1.0")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("notify: status %d", resp.StatusCode)
	}
	n.logger.Debug("notification sent", "event_type", string(event), "priority", priority(event))
	return nil
}

func priority(event Event) string {
	switch event {
	case EventValidationFailed, EventError:
		return "high"
	case EventTest:
		return "low"
	default:
		return "default"
	}
}

func tags(event Event) string {
	switch event {
	case EventIdentificationComplete:
		return "identify"
	case EventRipComplete:
		return "rip"
	case EventEncodeComplete:
		return "encode"
	case EventValidationFailed:
		return "validation,warning"
	case EventOrganizeComplete:
		return "organize"
	case EventQueueStarted, EventQueueCompleted:
		return "queue"
	case EventError:
		return "error"
	case EventUnidentifiedMedia:
		return "review"
	case EventTest:
		return "test"
	default:
		return ""
	}
}
