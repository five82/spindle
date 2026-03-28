package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

	// When Since is set, binary search to skip entries we've already seen.
	// Sequences are monotonically increasing in ring order.
	scanStart := 0
	if opts.Since > 0 {
		scanStart = b.bsearchSince(startIdx, opts.Since)
	}

	// Collect matching entries.
	var results []LogEntry
	for i := scanStart; i < b.count; i++ {
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

// bsearchSince returns the ring-relative offset of the first entry with seq > since.
// Must be called under b.mu.RLock. startIdx is the absolute ring index of the oldest entry.
func (b *LogBuffer) bsearchSince(startIdx int, since uint64) int {
	lo, hi := 0, b.count
	for lo < hi {
		mid := lo + (hi-lo)/2
		idx := (startIdx + mid) % b.cap
		if b.entries[idx].Seq <= since {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
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

// HydrateFromDir reads all spindle-*.log files in dir, parses JSON log lines,
// and loads entries into the buffer. Files are processed in lexicographic order
// (oldest first, since filenames contain timestamps). If total entries exceed
// buffer capacity, only the most recent entries are retained.
func (b *LogBuffer) HydrateFromDir(dir string) error {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read log directory: %w", err)
	}

	var entries []LogEntry
	for _, de := range dirEntries {
		name := de.Name()
		if de.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !strings.HasPrefix(name, "spindle-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		path := filepath.Join(dir, name)
		fileEntries, err := parseLogFile(path)
		if err != nil {
			continue // skip unreadable files
		}
		entries = append(entries, fileEntries...)
	}

	if len(entries) == 0 {
		return nil
	}

	// Keep only the most recent entries if we exceed capacity.
	if len(entries) > b.cap {
		entries = entries[len(entries)-b.cap:]
	}

	// Load into buffer under lock.
	b.mu.Lock()
	for i, e := range entries {
		e.Seq = uint64(i + 1)
		b.entries[i%b.cap] = e
	}
	b.count = len(entries)
	b.head = len(entries) % b.cap
	b.mu.Unlock()
	b.nextSeq.Store(uint64(len(entries) + 1))

	return nil
}

// parseLogFile reads a JSON-lines log file and returns parsed LogEntry values.
func parseLogFile(path string) ([]LogEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []LogEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		if e, ok := parseJSONLogLine(scanner.Bytes()); ok {
			entries = append(entries, e)
		}
	}
	return entries, scanner.Err()
}

// parseJSONLogLine parses a single slog JSON line into a LogEntry.
// Returns (entry, false) for malformed lines.
func parseJSONLogLine(line []byte) (LogEntry, bool) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return LogEntry{}, false
	}

	var e LogEntry
	fields := make(map[string]string)

	for k, v := range raw {
		switch k {
		case "time":
			if s, ok := v.(string); ok {
				e.Time = s
			}
		case "level":
			if s, ok := v.(string); ok {
				e.Level = s
			}
		case "msg":
			if s, ok := v.(string); ok {
				e.Msg = s
			}
		case "component":
			if s, ok := v.(string); ok {
				e.Component = s
			}
		case "stage":
			if s, ok := v.(string); ok {
				e.Stage = s
			}
		case "item_id":
			switch val := v.(type) {
			case float64:
				e.ItemID = int64(val)
			case json.Number:
				if n, err := val.Int64(); err == nil {
					e.ItemID = n
				}
			}
		case "lane":
			if s, ok := v.(string); ok {
				e.Lane = s
			}
		case "request":
			if s, ok := v.(string); ok {
				e.Request = s
			}
		default:
			fields[k] = fmt.Sprint(v)
		}
	}

	if len(fields) > 0 {
		e.Fields = fields
	}
	return e, true
}

func cloneAttrs(attrs []slog.Attr) []slog.Attr {
	if len(attrs) == 0 {
		return nil
	}
	c := make([]slog.Attr, len(attrs))
	copy(c, attrs)
	return c
}
