package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Event represents a notification event type.
type Event string

const (
	EventItemQueued             Event = "item_queued"
	EventIdentificationComplete Event = "identification_complete"
	EventRipCacheHit            Event = "rip_cache_hit"
	EventRipComplete            Event = "rip_complete"
	EventReviewRequired         Event = "review_required"
	EventPipelineComplete       Event = "pipeline_complete"
	EventError                  Event = "error"
	EventTest                   Event = "test"
)

// Notifier sends notifications via ntfy.
type Notifier struct {
	topic   string
	timeout time.Duration
	client  *http.Client
}

// New creates a Notifier. Returns nil if topic is empty (notifications disabled).
func New(topic string, timeoutSeconds int) *Notifier {
	topic = strings.TrimSpace(topic)
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
	}
}

// Send sends a notification. Returns nil if Notifier is nil (disabled).
func (n *Notifier) Send(ctx context.Context, event Event, title, message string) error {
	if n == nil {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.topic, strings.NewReader(message))
	if err != nil {
		return fmt.Errorf("notify: create request: %w", sanitizedRequestError(err))
	}

	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", title)
	req.Header.Set("Priority", priority(event))
	if t := tags(event); t != "" {
		req.Header.Set("Tags", t)
	}
	req.Header.Set("User-Agent", "Spindle")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: send: %w", sanitizedRequestError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		detail := strings.TrimSpace(string(body))
		if detail != "" {
			return fmt.Errorf("notify: status %d: %s", resp.StatusCode, detail)
		}
		return fmt.Errorf("notify: status %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// sanitizedRequestError removes the ntfy topic URL, which is effectively a
// credential, from errors returned by net/http.
func sanitizedRequestError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}
	return err
}

func priority(event Event) string {
	switch event {
	case EventError:
		return "high"
	case EventItemQueued, EventIdentificationComplete, EventRipCacheHit, EventTest:
		return "low"
	default:
		return "default"
	}
}

func tags(event Event) string {
	switch event {
	case EventItemQueued:
		return "inbox_tray"
	case EventIdentificationComplete:
		return "mag"
	case EventRipCacheHit, EventRipComplete:
		return "cd"
	case EventReviewRequired:
		return "warning"
	case EventPipelineComplete:
		return "heavy_check_mark"
	case EventError:
		return "rotating_light"
	case EventTest:
		return "test_tube"
	default:
		return ""
	}
}
