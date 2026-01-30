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
	Sequence      uint64            `json:"seq"`
	Timestamp     time.Time         `json:"ts"`
	Level         string            `json:"level"`
	Message       string            `json:"msg"`
	Component     string            `json:"component,omitempty"`
	Stage         string            `json:"stage,omitempty"`
	ItemID        int64             `json:"item_id,omitempty"`
	Lane          string            `json:"lane,omitempty"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	Fields        map[string]string `json:"fields,omitempty"`
	Details       []DetailField     `json:"details,omitempty"`
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
	sinks    []LogEventSink
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

// LogEventSink receives published log events (for persistence, etc.).
type LogEventSink interface {
	Append(LogEvent)
}

// AddSink wires an additional sink that receives every published event.
func (h *StreamHub) AddSink(sink LogEventSink) {
	if h == nil || sink == nil {
		return
	}
	h.mu.Lock()
	h.sinks = append(h.sinks, sink)
	h.mu.Unlock()
}

// Publish appends a new log event to the hub.
func (h *StreamHub) Publish(evt LogEvent) {
	if h == nil {
		return
	}
	h.mu.Lock()
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
	sinks := append([]LogEventSink(nil), h.sinks...)
	h.cond.Broadcast()
	h.mu.Unlock()

	for _, sink := range sinks {
		sink.Append(evt)
	}
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

// FirstSequence reports the smallest sequence number still buffered.
func (h *StreamHub) FirstSequence() uint64 {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.buffer) == 0 {
		return h.nextSeq
	}
	return h.buffer[0].Sequence
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
	next  slog.Handler
	hub   *StreamHub
	attrs []slog.Attr // accumulated attrs from WithAttrs calls
}

func newStreamHandler(next slog.Handler, hub *StreamHub) slog.Handler {
	if hub == nil || next == nil {
		return next
	}
	return &streamHandler{next: next, hub: hub, attrs: nil}
}

func (h *streamHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *streamHandler) Handle(ctx context.Context, record slog.Record) error {
	if h.hub != nil {
		h.hub.Publish(eventFromRecordWithAttrs(record, h.attrs))
	}
	return h.next.Handle(ctx, record.Clone())
}

func (h *streamHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Accumulate attrs so Handle() can include them in LogEvents
	newAttrs := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	newAttrs = append(newAttrs, h.attrs...)
	newAttrs = append(newAttrs, attrs...)
	return &streamHandler{
		next:  h.next.WithAttrs(attrs),
		hub:   h.hub,
		attrs: newAttrs,
	}
}

func (h *streamHandler) WithGroup(name string) slog.Handler {
	return &streamHandler{
		next: h.next.WithGroup(name),
		hub:  h.hub,
	}
}

func eventFromRecordWithAttrs(record slog.Record, preAttrs []slog.Attr) LogEvent {
	event := LogEvent{
		Timestamp: record.Time,
		Level:     strings.ToUpper(record.Level.String()),
		Message:   strings.TrimSpace(record.Message),
		Fields:    make(map[string]string),
	}

	// Process an attribute and update the event
	processAttr := func(attr slog.Attr) {
		key := strings.TrimSpace(attr.Key)
		if key == "" {
			return
		}
		switch key {
		case FieldItemID:
			event.ItemID = attr.Value.Int64()
		case FieldStage:
			event.Stage = attrString(attr.Value)
		case FieldLane:
			event.Lane = attrString(attr.Value)
		case FieldCorrelationID:
			event.CorrelationID = attrString(attr.Value)
		case "component":
			event.Component = attrString(attr.Value)
		default:
			if event.Fields == nil {
				event.Fields = make(map[string]string)
			}
			event.Fields[key] = attrString(attr.Value)
		}
	}

	// Process pre-accumulated attrs first (from WithAttrs calls)
	for _, attr := range preAttrs {
		processAttr(attr)
	}

	// Then process record attrs (call-site attrs override pre-attrs)
	var attrs []kv
	record.Attrs(func(attr slog.Attr) bool {
		processAttr(attr)
		key := strings.TrimSpace(attr.Key)
		if key != "" {
			attrs = append(attrs, kv{key: key, value: attr.Value})
		}
		return true
	})

	if len(attrs) > 0 {
		if info, _ := selectInfoFields(attrs, infoAttrLimit, false); len(info) > 0 {
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
