package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/five82/spindle/internal/logs"
)

// Event represents a notification event type.
type Event string

const (
	EventItemQueued             Event = "item_queued"
	EventIdentificationComplete Event = "identification_complete"
	EventRipCacheHit            Event = "rip_cache_hit"
	EventRipComplete            Event = "rip_complete"
	EventEncodeComplete         Event = "encode_complete"
	EventReviewRequired         Event = "review_required"
	EventPipelineComplete       Event = "pipeline_complete"
	EventQueueStarted           Event = "queue_started"
	EventQueueCompleted         Event = "queue_completed"
	EventError                  Event = "error"
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
	logger = logs.Default(logger)
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
	return nil
}

func priority(event Event) string {
	switch event {
	case EventReviewRequired, EventError:
		return "high"
	case EventRipCacheHit, EventTest:
		return "low"
	default:
		return "default"
	}
}

func tags(event Event) string {
	switch event {
	case EventItemQueued, EventQueueStarted, EventQueueCompleted:
		return "queue"
	case EventIdentificationComplete:
		return "identify"
	case EventRipCacheHit:
		return "rip,cache"
	case EventRipComplete:
		return "rip"
	case EventEncodeComplete:
		return "encode"
	case EventReviewRequired:
		return "review,warning"
	case EventPipelineComplete:
		return "complete"
	case EventError:
		return "error"
	case EventTest:
		return "test"
	default:
		return ""
	}
}
