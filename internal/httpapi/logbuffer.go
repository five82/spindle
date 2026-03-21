package httpapi

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const defaultLogBufferCapacity = 10000

// LogEntry is a single structured log event stored in the buffer.
type LogEntry struct {
	Seq       uint64            `json:"seq"`
	Time      string            `json:"ts"`
	Level     string            `json:"level"`
	Msg       string            `json:"msg"`
	Component string            `json:"component,omitempty"`
	Stage     string            `json:"stage,omitempty"`
	ItemID    int64             `json:"item_id,omitempty"`
	Lane      string            `json:"lane,omitempty"`
	Request   string            `json:"request,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	Details   []DetailField     `json:"details,omitempty"`
}

// DetailField is a label/value pair for structured log details.
type DetailField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// LogBuffer is a goroutine-safe ring buffer for structured log events.
type LogBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
	head    int // write position (wraps)
	count   int
	cap     int
	nextSeq atomic.Uint64
}

// NewLogBuffer creates a log buffer with the given capacity.
func NewLogBuffer(capacity int) *LogBuffer {
	if capacity <= 0 {
		capacity = defaultLogBufferCapacity
	}
	buf := &LogBuffer{
		entries: make([]LogEntry, capacity),
		cap:     capacity,
	}
	buf.nextSeq.Store(1) // sequences start at 1
	return buf
}

// Append adds an entry to the buffer and assigns it a sequence number.
func (b *LogBuffer) Append(entry LogEntry) {
	entry.Seq = b.nextSeq.Add(1) - 1
	b.mu.Lock()
	b.entries[b.head] = entry
	b.head = (b.head + 1) % b.cap
	if b.count < b.cap {
		b.count++
	}
	b.mu.Unlock()
}

// LogQueryOpts configures a log buffer query.
type LogQueryOpts struct {
	Since      uint64 // return entries with seq > Since
	Limit      int    // max entries to return (default 200)
	Tail       bool   // return the most recent entries
	ItemID     int64  // filter by item ID (0 = no filter)
	Component  string // filter by component (case-insensitive)
	Lane       string // filter by lane (case-insensitive)
	Request    string // filter by request ID (case-insensitive)
	Level      string // minimum log level (debug, info, warn, error)
	DaemonOnly bool   // only entries with ItemID == 0
}

// levelRank maps log levels to numeric ranks for >= filtering.
func levelRank(level string) int {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return 0
	case "INFO":
		return 1
	case "WARN", "WARNING":
		return 2
	case "ERROR":
		return 3
	default:
		return -1
	}
}

// Query returns entries matching the filter, plus the next sequence cursor.
func (b *LogBuffer) Query(opts LogQueryOpts) ([]LogEntry, uint64) {
	if opts.Limit <= 0 {
		opts.Limit = 200
	}

	minLevel := -1
	if opts.Level != "" {
		minLevel = levelRank(opts.Level)
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.count == 0 {
		return nil, b.nextSeq.Load()
	}

	// Calculate start index in the ring buffer.
	startIdx := b.head - b.count
	if startIdx < 0 {
		startIdx += b.cap
	}

	// Collect matching entries.
	var results []LogEntry
	for i := 0; i < b.count; i++ {
		idx := (startIdx + i) % b.cap
		e := b.entries[idx]

		if opts.Since > 0 && e.Seq <= opts.Since {
			continue
		}
		if !b.matchesFilter(e, opts, minLevel) {
			continue
		}
		results = append(results, e)
	}

	// Apply tail: keep only the last N entries.
	if opts.Tail && len(results) > opts.Limit {
		results = results[len(results)-opts.Limit:]
	} else if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	next := b.nextSeq.Load()
	if len(results) > 0 {
		next = results[len(results)-1].Seq + 1
	}

	return results, next
}

func (b *LogBuffer) matchesFilter(e LogEntry, opts LogQueryOpts, minLevel int) bool {
	if opts.ItemID > 0 && e.ItemID != opts.ItemID {
		return false
	}
	if opts.DaemonOnly && e.ItemID != 0 {
		return false
	}
	if opts.Component != "" && !strings.EqualFold(e.Component, opts.Component) {
		return false
	}
	if opts.Lane != "" && !strings.EqualFold(e.Lane, opts.Lane) {
		return false
	}
	if opts.Request != "" && !strings.EqualFold(e.Request, opts.Request) {
		return false
	}
	if minLevel >= 0 && levelRank(e.Level) < minLevel {
		return false
	}
	return true
}

// LogHandler is an slog.Handler that captures log records into a LogBuffer
// while delegating to an inner handler for normal output.
type LogHandler struct {
	inner  slog.Handler
	buffer *LogBuffer
	attrs  []slog.Attr // pre-resolved attributes from WithAttrs
	group  string      // current group prefix
}

// NewLogHandler wraps an inner slog.Handler and captures records to the buffer.
func NewLogHandler(inner slog.Handler, buffer *LogBuffer) *LogHandler {
	return &LogHandler{inner: inner, buffer: buffer}
}

func (h *LogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *LogHandler) Handle(ctx context.Context, record slog.Record) error {
	entry := LogEntry{
		Time:  record.Time.UTC().Format(time.RFC3339Nano),
		Level: record.Level.String(),
		Msg:   record.Message,
	}

	fields := make(map[string]string)

	// Process pre-resolved attrs from WithAttrs.
	for _, a := range h.attrs {
		h.extractAttr(&entry, fields, a)
	}

	// Process record-level attrs.
	record.Attrs(func(a slog.Attr) bool {
		h.extractAttr(&entry, fields, a)
		return true
	})

	if len(fields) > 0 {
		entry.Fields = fields
	}

	h.buffer.Append(entry)

	return h.inner.Handle(ctx, record)
}

func (h *LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogHandler{
		inner:  h.inner.WithAttrs(attrs),
		buffer: h.buffer,
		attrs:  append(cloneAttrs(h.attrs), attrs...),
		group:  h.group,
	}
}

func (h *LogHandler) WithGroup(name string) slog.Handler {
	prefix := name
	if h.group != "" {
		prefix = h.group + "." + name
	}
	return &LogHandler{
		inner:  h.inner.WithGroup(name),
		buffer: h.buffer,
		attrs:  cloneAttrs(h.attrs),
		group:  prefix,
	}
}

// extractAttr routes known attribute keys to LogEntry fields, everything else to Fields.
func (h *LogHandler) extractAttr(entry *LogEntry, fields map[string]string, a slog.Attr) {
	key := a.Key
	if h.group != "" {
		key = h.group + "." + key
	}

	val := a.Value.Resolve()

	switch key {
	case "component":
		entry.Component = val.String()
	case "stage":
		entry.Stage = val.String()
	case "item_id":
		entry.ItemID = val.Int64()
	case "lane":
		entry.Lane = val.String()
	case "request":
		entry.Request = val.String()
	default:
		if val.Kind() == slog.KindGroup {
			// Flatten group attrs.
			for _, ga := range val.Group() {
				gaKey := key + "." + ga.Key
				fields[gaKey] = ga.Value.Resolve().String()
			}
		} else {
			fields[key] = val.String()
		}
	}
}

func cloneAttrs(attrs []slog.Attr) []slog.Attr {
	if len(attrs) == 0 {
		return nil
	}
	c := make([]slog.Attr, len(attrs))
	copy(c, attrs)
	return c
}
