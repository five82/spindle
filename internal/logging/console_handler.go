package logging

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type prettyHandler struct {
	mu        sync.Mutex
	writer    io.Writer
	level     *slog.LevelVar
	attrs     []slog.Attr
	groups    []string
	addSource bool
	infoCache map[string]map[string]string
}

func newPrettyHandler(w io.Writer, lvl *slog.LevelVar, addSource bool) slog.Handler {
	return &prettyHandler{writer: w, level: lvl, addSource: addSource, infoCache: make(map[string]map[string]string)}
}

func (h *prettyHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *prettyHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Level < h.level.Level() {
		return nil
	}

	timestamp := record.Time
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	kvs := make([]kv, 0, record.NumAttrs()+len(h.attrs))
	flattenAttrs(&kvs, h.groups, h.attrs)
	record.Attrs(func(attr slog.Attr) bool {
		flattenAttr(&kvs, h.groups, attr)
		return true
	})

	allAttrs := make([]kv, len(kvs))
	copy(allAttrs, kvs)

	var component string
	var itemID string
	var stage string
	var lane string
	filtered := make([]kv, 0, len(kvs))
	for _, kv := range kvs {
		if kv.key == "component" {
			if component == "" {
				component = attrString(kv.value)
			}
			continue
		}
		if kv.key == FieldItemID && itemID == "" {
			itemID = attrString(kv.value)
		}
		if kv.key == FieldStage && stage == "" {
			stage = attrString(kv.value)
		}
		if kv.key == FieldLane && lane == "" {
			lane = attrString(kv.value)
		}
		filtered = append(filtered, kv)
	}

	filtered = dedupeKVsByKey(filtered)
	allAttrs = dedupeKVsByKey(allAttrs)

	message := strings.TrimSpace(record.Message)
	if message == "" {
		message = "(no message)"
	}

	var buf bytes.Buffer
	buf.Grow(256 + len(filtered)*32)

	h.mu.Lock()
	defer h.mu.Unlock()
	if record.Level < slog.LevelInfo {
		h.writeDebug(&buf, timestamp, record.Level, component, lane, itemID, stage, message, record.Source(), allAttrs)
	} else {
		h.writeInfo(&buf, timestamp, record.Level, component, lane, itemID, stage, message, record.Source(), filtered)
	}
	_, err := h.writer.Write(buf.Bytes())
	return err
}

func (h *prettyHandler) writeInfo(buf *bytes.Buffer, ts time.Time, level slog.Level, component, lane, itemID, stage, message string, src *slog.Source, attrs []kv) {
	writeLogHeader(buf, ts, level, component, lane, itemID, stage, message, h.addSource, src)
	fields, hidden := selectInfoFields(attrs, 0, true)
	summaryKey := infoSummaryKey(component, itemID, stage, attrs)
	fields, hidden = h.filterRepeatedInfo(summaryKey, fields, hidden, level)
	if len(fields) == 0 && hidden == 0 {
		buf.WriteByte('\n')
		return
	}
	buf.WriteByte('\n')
	for _, field := range fields {
		buf.WriteString("    - ")
		buf.WriteString(field.label)
		buf.WriteString(": ")
		buf.WriteString(field.value)
		buf.WriteByte('\n')
	}
	if hidden > 0 {
		buf.WriteString("    + ")
		buf.WriteString(strconv.Itoa(hidden))
		buf.WriteString(" more field")
		if hidden != 1 {
			buf.WriteByte('s')
		}
		buf.WriteString(" hidden")
		buf.WriteByte('\n')
	}
}

func (h *prettyHandler) writeDebug(buf *bytes.Buffer, ts time.Time, level slog.Level, component, lane, itemID, stage, message string, src *slog.Source, attrs []kv) {
	writeLogHeader(buf, ts, level, component, lane, itemID, stage, message, h.addSource, src)
	if len(attrs) == 0 {
		buf.WriteByte('\n')
		return
	}
	buf.WriteByte('\n')
	for _, kv := range attrs {
		if kv.key == "" {
			continue
		}
		buf.WriteString("    ")
		buf.WriteString(kv.key)
		buf.WriteString(": ")
		buf.WriteString(formatValue(kv.value))
		buf.WriteByte('\n')
	}
}

func writeLogHeader(buf *bytes.Buffer, ts time.Time, level slog.Level, component, lane, itemID, stage, message string, addSource bool, src *slog.Source) {
	buf.WriteString(formatTimestamp(ts))
	buf.WriteByte(' ')
	buf.WriteString(levelLabel(level))
	if component != "" {
		buf.WriteString(" [")
		buf.WriteString(component)
		buf.WriteByte(']')
	}
	if subject := composeSubject(lane, itemID, stage); subject != "" {
		buf.WriteByte(' ')
		buf.WriteString(subject)
	}
	if message != "" {
		buf.WriteString(" – ")
		buf.WriteString(message)
	}
	if addSource && src != nil {
		buf.WriteString(" [")
		buf.WriteString(filepath.Base(src.File))
		buf.WriteByte(':')
		buf.WriteString(strconv.Itoa(src.Line))
		buf.WriteByte(']')
	}
}

func composeSubject(lane, itemID, stage string) string {
	lane = strings.TrimSpace(lane)
	itemID = strings.TrimSpace(itemID)
	stage = strings.TrimSpace(stage)
	parts := make([]string, 0, 3)
	if lane != "" {
		var formattedLane string
		if len(lane) > 1 {
			formattedLane = strings.ToUpper(lane[:1]) + strings.ToLower(lane[1:])
		} else {
			formattedLane = strings.ToUpper(lane)
		}
		parts = append(parts, formattedLane)
	}
	switch {
	case itemID != "" && stage != "":
		parts = append(parts, "Item #"+itemID+" ("+stage+")")
	case itemID != "":
		parts = append(parts, "Item #"+itemID)
	case stage != "":
		parts = append(parts, stage)
	}
	return strings.Join(parts, " · ")
}

func (h *prettyHandler) filterRepeatedInfo(key string, fields []infoField, hidden int, level slog.Level) ([]infoField, int) {
	if key == "" || len(fields) == 0 {
		return fields, hidden
	}
	cache := h.ensureInfoCache(key)
	if level > slog.LevelInfo {
		for _, field := range fields {
			cache[field.label] = field.value
		}
		return fields, hidden
	}
	filtered := make([]infoField, 0, len(fields))
	for _, field := range fields {
		if prev, ok := cache[field.label]; ok && prev == field.value {
			continue
		}
		cache[field.label] = field.value
		filtered = append(filtered, field)
	}
	return filtered, hidden
}

func (h *prettyHandler) ensureInfoCache(key string) map[string]string {
	if cache, ok := h.infoCache[key]; ok {
		return cache
	}
	cache := make(map[string]string)
	h.infoCache[key] = cache
	return cache
}

func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := h.clone()
	clone.attrs = append(clone.attrs, attrs...)
	return clone
}

func (h *prettyHandler) WithGroup(name string) slog.Handler {
	clone := h.clone()
	clone.groups = append(clone.groups, name)
	return clone
}

func (h *prettyHandler) clone() *prettyHandler {
	clone := &prettyHandler{
		writer:    h.writer,
		level:     h.level,
		addSource: h.addSource,
		infoCache: h.infoCache,
	}
	if len(h.attrs) > 0 {
		clone.attrs = make([]slog.Attr, len(h.attrs))
		copy(clone.attrs, h.attrs)
	}
	if len(h.groups) > 0 {
		clone.groups = make([]string, len(h.groups))
		copy(clone.groups, h.groups)
	}
	return clone
}

type kv struct {
	key   string
	value slog.Value
}

func dedupeKVsByKey(attrs []kv) []kv {
	if len(attrs) < 2 {
		return attrs
	}
	positions := make(map[string]int, len(attrs))
	deduped := make([]kv, 0, len(attrs))
	for _, attr := range attrs {
		if attr.key == "" {
			continue
		}
		if pos, ok := positions[attr.key]; ok {
			deduped[pos].value = attr.value
			continue
		}
		positions[attr.key] = len(deduped)
		deduped = append(deduped, attr)
	}
	return deduped
}

func flattenAttrs(dst *[]kv, prefix []string, attrs []slog.Attr) {
	for _, attr := range attrs {
		flattenAttr(dst, prefix, attr)
	}
}

func flattenAttr(dst *[]kv, prefix []string, attr slog.Attr) {
	if attr.Equal(slog.Attr{}) {
		return
	}
	attr.Value = attr.Value.Resolve()
	switch attr.Value.Kind() {
	case slog.KindGroup:
		values := attr.Value.Group()
		nextPrefix := prefix
		if attr.Key != "" {
			nextPrefix = appendPrefix(prefix, attr.Key)
		}
		flattenAttrs(dst, nextPrefix, values)
	default:
		key := attr.Key
		if len(prefix) > 0 {
			if key != "" {
				key = strings.Join(append(prefix, key), ".")
			} else {
				key = strings.Join(prefix, ".")
			}
		}
		if key == "" {
			key = attr.Key
		}
		*dst = append(*dst, kv{key: key, value: attr.Value})
	}
}

func appendPrefix(prefix []string, value string) []string {
	if len(prefix) == 0 {
		return []string{value}
	}
	out := make([]string, len(prefix)+1)
	copy(out, prefix)
	out[len(prefix)] = value
	return out
}

func levelLabel(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "ERROR"
	case level >= slog.LevelWarn:
		return "WARN"
	case level >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}
