package logging

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// LogEvent represents a structured log line published to the streaming hub.
type LogEvent struct {
	Sequence  uint64            `json:"seq"`
	Timestamp time.Time         `json:"ts"`
	Level     string            `json:"level"`
	Message   string            `json:"msg"`
	Component string            `json:"component,omitempty"`
	Stage     string            `json:"stage,omitempty"`
	ItemID    int64             `json:"item_id,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	Details   []DetailField     `json:"details,omitempty"`
}

// DetailField mirrors the console handler's info bullet lines.
type DetailField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// StreamHub stores recent log events and wakes waiters when new events arrive.
type StreamHub struct {
	mu       sync.Mutex
	cond     *sync.Cond
	capacity int
	buffer   []LogEvent
	nextSeq  uint64
}

// NewStreamHub constructs a bounded in-memory log fan-out buffer.
func NewStreamHub(capacity int) *StreamHub {
	if capacity <= 0 {
		capacity = 512
	}
	h := &StreamHub{capacity: capacity}
	h.cond = sync.NewCond(&h.mu)
	return h
}

// Publish appends a new log event to the hub.
func (h *StreamHub) Publish(evt LogEvent) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	h.nextSeq++
	evt.Sequence = h.nextSeq
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}

	if len(h.buffer) == h.capacity {
		copy(h.buffer, h.buffer[1:])
		h.buffer = h.buffer[:h.capacity-1]
	}
	h.buffer = append(h.buffer, evt)
	h.cond.Broadcast()
}

// Fetch returns all events with sequence greater than since. When wait is true,
// Fetch blocks until at least one event is available or the context ends.
func (h *StreamHub) Fetch(ctx context.Context, since uint64, limit int, wait bool) ([]LogEvent, uint64, error) {
	if h == nil {
		return nil, since, nil
	}
	if limit <= 0 || limit > h.capacity {
		limit = h.capacity
	}

	cancelWait := make(chan struct{})
	if wait && ctx != nil && ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				h.cond.Broadcast()
			case <-cancelWait:
			}
		}()
	}
	defer close(cancelWait)

	h.mu.Lock()
	defer h.mu.Unlock()

	for {
		events, next := h.snapshotLocked(since, limit)
		if len(events) > 0 || !wait {
			return events, next, contextError(ctx)
		}
		if err := contextError(ctx); err != nil {
			return nil, next, err
		}
		h.cond.Wait()
		if err := contextError(ctx); err != nil {
			return nil, next, err
		}
	}
}

// Tail returns the most recent limit events without blocking.
func (h *StreamHub) Tail(limit int) ([]LogEvent, uint64) {
	if h == nil {
		return nil, 0
	}
	if limit <= 0 || limit > h.capacity {
		limit = h.capacity
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.buffer) == 0 {
		return nil, h.nextSeq
	}
	start := len(h.buffer) - limit
	if start < 0 {
		start = 0
	}
	out := make([]LogEvent, len(h.buffer)-start)
	copy(out, h.buffer[start:])
	return out, h.nextSeq
}

func (h *StreamHub) snapshotLocked(since uint64, limit int) ([]LogEvent, uint64) {
	if len(h.buffer) == 0 {
		return nil, h.nextSeq
	}
	startIdx := 0
	for i, evt := range h.buffer {
		if evt.Sequence > since {
			startIdx = i
			break
		}
		if i == len(h.buffer)-1 {
			return nil, h.nextSeq
		}
	}
	end := startIdx + limit
	if end > len(h.buffer) {
		end = len(h.buffer)
	}
	out := make([]LogEvent, end-startIdx)
	copy(out, h.buffer[startIdx:end])
	return out, h.nextSeq
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

type streamHandler struct {
	next slog.Handler
	hub  *StreamHub
}

func newStreamHandler(next slog.Handler, hub *StreamHub) slog.Handler {
	if hub == nil || next == nil {
		return next
	}
	return &streamHandler{next: next, hub: hub}
}

func (h *streamHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *streamHandler) Handle(ctx context.Context, record slog.Record) error {
	if h.hub != nil {
		h.hub.Publish(eventFromRecord(record))
	}
	return h.next.Handle(ctx, record.Clone())
}

func (h *streamHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &streamHandler{
		next: h.next.WithAttrs(attrs),
		hub:  h.hub,
	}
}

func (h *streamHandler) WithGroup(name string) slog.Handler {
	return &streamHandler{
		next: h.next.WithGroup(name),
		hub:  h.hub,
	}
}

func eventFromRecord(record slog.Record) LogEvent {
	event := LogEvent{
		Timestamp: record.Time,
		Level:     strings.ToUpper(record.Level.String()),
		Message:   strings.TrimSpace(record.Message),
		Fields:    make(map[string]string),
	}

	var attrs []kv
	record.Attrs(func(attr slog.Attr) bool {
		key := strings.TrimSpace(attr.Key)
		if key == "" {
			return true
		}
		switch key {
		case FieldItemID:
			event.ItemID = attr.Value.Int64()
			return true
		case FieldStage:
			event.Stage = attrString(attr.Value)
			return true
		case "component":
			event.Component = attrString(attr.Value)
			return true
		}
		if event.Fields == nil {
			event.Fields = make(map[string]string)
		}
		event.Fields[key] = attrString(attr.Value)
		attrs = append(attrs, kv{key: key, value: attr.Value})
		return true
	})

	if len(attrs) > 0 {
		if info, _ := selectInfoFields(attrs); len(info) > 0 {
			event.Details = make([]DetailField, 0, len(info))
			for _, field := range info {
				event.Details = append(event.Details, DetailField{
					Label: field.label,
					Value: field.value,
				})
			}
		}
	}

	return event
}
