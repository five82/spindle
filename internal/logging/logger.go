package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"spindle/internal/config"
)

// Options describes logger construction parameters.
type Options struct {
	Level            string
	Format           string
	OutputPaths      []string
	ErrorOutputPaths []string
	Development      bool
}

// New constructs a slog logger using the provided options.
func New(opts Options) (*slog.Logger, error) {
	level := parseLevel(opts.Level)
	levelVar := new(slog.LevelVar)
	levelVar.Set(level)

	outputWriter, err := openWriters(
		defaultSlice(opts.OutputPaths, []string{"stdout"}),
		defaultSlice(opts.ErrorOutputPaths, []string{"stderr"}),
	)
	if err != nil {
		return nil, err
	}

	addSource := opts.Development || level <= slog.LevelDebug

	format := strings.ToLower(strings.TrimSpace(opts.Format))
	if format == "" {
		format = "console"
	}

	var handler slog.Handler
	switch format {
	case "json":
		handler, err = newJSONHandler(outputWriter, levelVar, addSource)
		if err != nil {
			return nil, err
		}
	case "console":
		handler = newPrettyHandler(outputWriter, levelVar, addSource)
	default:
		return nil, fmt.Errorf("log format: unsupported value %q", opts.Format)
	}

	return slog.New(handler), nil
}

// NewFromConfig creates a logger using application config defaults.
func NewFromConfig(cfg *config.Config) (*slog.Logger, error) {
	if cfg == nil {
		return New(Options{Level: "info", Format: "console", OutputPaths: []string{"stdout"}, ErrorOutputPaths: []string{"stderr"}})
	}

	outputPaths := []string{"stdout"}
	errorOutputs := []string{"stderr"}
	if cfg.LogDir != "" {
		if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
			return nil, fmt.Errorf("ensure log directory: %w", err)
		}
		logPath := filepath.Join(cfg.LogDir, "spindle.log")
		outputPaths = append(outputPaths, logPath)
		errorOutputs = append(errorOutputs, logPath)
	}

	opts := Options{
		Level:            cfg.LogLevel,
		Format:           cfg.LogFormat,
		OutputPaths:      outputPaths,
		ErrorOutputPaths: errorOutputs,
		Development:      false,
	}
	return New(opts)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "dpanic", "panic", "fatal": // map to error semantics
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}

func defaultSlice(value []string, fallback []string) []string {
	if len(value) == 0 {
		cp := make([]string, len(fallback))
		copy(cp, fallback)
		return cp
	}
	cp := make([]string, len(value))
	copy(cp, value)
	return cp
}

func openWriters(outputPaths []string, errorPaths []string) (io.Writer, error) {
	seen := map[string]struct{}{}
	var writers []io.Writer
	combined := append([]string{}, outputPaths...)
	combined = append(combined, errorPaths...)

	for _, path := range combined {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}

		switch trimmed {
		case "stdout":
			writers = append(writers, os.Stdout)
		case "stderr":
			writers = append(writers, os.Stderr)
		default:
			if err := ensureLogDir(trimmed); err != nil {
				return nil, err
			}
			file, err := os.OpenFile(trimmed, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o664)
			if err != nil {
				return nil, fmt.Errorf("open log file %s: %w", trimmed, err)
			}
			writers = append(writers, file)
		}
	}

	if len(writers) == 0 {
		return os.Stdout, nil
	}

	if len(writers) == 1 {
		return writers[0], nil
	}
	return io.MultiWriter(writers...), nil
}

func ensureLogDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func newJSONHandler(w io.Writer, lvl *slog.LevelVar, addSource bool) (slog.Handler, error) {
	opts := slog.HandlerOptions{
		Level:     lvl,
		AddSource: addSource,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			switch attr.Key {
			case slog.TimeKey:
				attr.Key = "ts"
				if attr.Value.Kind() == slog.KindTime {
					attr.Value = slog.StringValue(attr.Value.Time().UTC().Format(time.RFC3339))
				}
			case slog.LevelKey:
				attr.Key = "level"
				attr.Value = slog.StringValue(strings.ToLower(attr.Value.String()))
			case slog.MessageKey:
				attr.Key = "msg"
			case slog.SourceKey:
				if src, ok := attr.Value.Any().(*slog.Source); ok && src != nil {
					attr.Value = slog.StringValue(fmt.Sprintf("%s:%d", filepath.Base(src.File), src.Line))
				}
			}
			return attr
		},
	}

	return slog.NewJSONHandler(w, &opts), nil
}

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
		filtered = append(filtered, kv)
	}

	message := strings.TrimSpace(record.Message)
	if message == "" {
		message = "(no message)"
	}

	var buf bytes.Buffer
	buf.Grow(256 + len(filtered)*32)

	h.mu.Lock()
	defer h.mu.Unlock()
	if record.Level < slog.LevelInfo {
		h.writeDebug(&buf, timestamp, record.Level, component, itemID, stage, message, record.Source(), allAttrs)
	} else {
		h.writeInfo(&buf, timestamp, record.Level, component, itemID, stage, message, record.Source(), filtered)
	}
	_, err := h.writer.Write(buf.Bytes())
	return err
}

func (h *prettyHandler) writeInfo(buf *bytes.Buffer, ts time.Time, level slog.Level, component, itemID, stage, message string, src *slog.Source, attrs []kv) {
	writeLogHeader(buf, ts, level, component, itemID, stage, message, h.addSource, src)
	fields, hidden := selectInfoFields(attrs)
	summaryKey := infoSummaryKey(component, itemID, stage, attrs)
	fields, hidden = h.filterRepeatedInfo(summaryKey, fields, hidden, level)
	if len(fields) == 0 {
		buf.WriteByte('\n')
		return
	}
	if hidden > 0 {
		buf.WriteString(" (+")
		buf.WriteString(strconv.Itoa(hidden))
		buf.WriteString(" details)")
	}
	buf.WriteByte('\n')
	for _, field := range fields {
		buf.WriteString("    - ")
		buf.WriteString(field.label)
		buf.WriteString(": ")
		buf.WriteString(field.value)
		buf.WriteByte('\n')
	}
}

func (h *prettyHandler) writeDebug(buf *bytes.Buffer, ts time.Time, level slog.Level, component, itemID, stage, message string, src *slog.Source, attrs []kv) {
	writeLogHeader(buf, ts, level, component, itemID, stage, message, h.addSource, src)
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

func writeLogHeader(buf *bytes.Buffer, ts time.Time, level slog.Level, component, itemID, stage, message string, addSource bool, src *slog.Source) {
	buf.WriteString(ts.UTC().Format(time.RFC3339))
	buf.WriteByte(' ')
	buf.WriteString(levelLabel(level))
	if component != "" {
		buf.WriteString(" [")
		buf.WriteString(component)
		buf.WriteByte(']')
	}
	if subject := composeSubject(itemID, stage); subject != "" {
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

func composeSubject(itemID, stage string) string {
	itemID = strings.TrimSpace(itemID)
	stage = strings.TrimSpace(stage)
	if itemID == "" && stage == "" {
		return ""
	}
	if itemID != "" && stage != "" {
		return "Item #" + itemID + " (" + stage + ")"
	}
	if itemID != "" {
		return "Item #" + itemID
	}
	return stage
}

type infoField struct {
	label string
	value string
}

const infoAttrLimit = 4

var infoHighlightKeys = []string{
	"disc_title",
	"disc_label",
	"processing_status",
	"progress_stage",
	"progress_message",
	"status",
	"disc_type",
	"runtime_minutes",
	"studio",
}

func selectInfoFields(attrs []kv) ([]infoField, int) {
	if len(attrs) == 0 {
		return nil, 0
	}
	used := make([]bool, len(attrs))
	formatted := make([]string, len(attrs))
	formattedSet := make([]bool, len(attrs))
	ensureValue := func(idx int) string {
		if !formattedSet[idx] {
			formatted[idx] = formatValue(attrs[idx].value)
			formattedSet[idx] = true
		}
		return formatted[idx]
	}
	result := make([]infoField, 0, infoAttrLimit)
	hidden := 0

	for _, key := range infoHighlightKeys {
		if len(result) >= infoAttrLimit {
			break
		}
		for idx, attr := range attrs {
			if used[idx] || attr.key != key {
				continue
			}
			used[idx] = true
			if skipInfoKey(attr.key) {
				break
			}
			if isDebugOnlyKey(attr.key) {
				hidden++
				break
			}
			val := ensureValue(idx)
			if shouldHideInfoValue(val) {
				hidden++
				break
			}
			result = append(result, infoField{label: displayLabel(attr.key), value: val})
			break
		}
	}

	for idx, attr := range attrs {
		if used[idx] {
			continue
		}
		used[idx] = true
		if skipInfoKey(attr.key) {
			continue
		}
		if isDebugOnlyKey(attr.key) {
			hidden++
			continue
		}
		val := ensureValue(idx)
		if shouldHideInfoValue(val) {
			hidden++
			continue
		}
		if len(result) < infoAttrLimit {
			result = append(result, infoField{label: displayLabel(attr.key), value: val})
		} else {
			hidden++
		}
	}

	return result, hidden
}

func skipInfoKey(key string) bool {
	switch key {
	case "", FieldItemID, FieldStage, "component":
		return true
	default:
		return false
	}
}

func isDebugOnlyKey(key string) bool {
	if key == "" {
		return true
	}
	switch key {
	case FieldCorrelationID,
		"fingerprint",
		"device",
		"source_path",
		"destination_dir",
		"tmdb_id",
		"runtime_range",
		"volume_identifier",
		"bd_info_available",
		"makemkv_enabled",
		"is_blu_ray",
		"has_aacs",
		"vote_average",
		"vote_count",
		"popularity":
		return true
	}
	if strings.Contains(key, "correlation") {
		return true
	}
	if strings.HasSuffix(key, "_id") && key != FieldItemID {
		return true
	}
	if strings.Contains(key, "_path") || strings.Contains(key, "_dir") {
		return true
	}
	if strings.Contains(key, "fingerprint") || strings.Contains(key, "tmdb") {
		return true
	}
	return false
}

func shouldHideInfoValue(value string) bool {
	return len(value) > 120
}

func displayLabel(key string) string {
	switch key {
	case FieldItemID:
		return "Item"
	case FieldStage:
		return "Stage"
	case "disc_title":
		return "Disc"
	case "disc_label":
		return "Label"
	case "processing_status":
		return "Status"
	case "progress_stage":
		return "Progress Stage"
	case "progress_message":
		return "Progress"
	case "disc_type":
		return "Type"
	case "runtime_minutes":
		return "Runtime"
	default:
		return titleizeKey(key)
	}
}

func titleizeKey(key string) string {
	if key == "" {
		return ""
	}
	parts := strings.FieldsFunc(key, func(r rune) bool {
		return r == '_' || r == '-'
	})
	if len(parts) == 0 {
		return strings.ToUpper(key[:1]) + strings.ToLower(key[1:])
	}
	for i, part := range parts {
		parts[i] = capitalizeASCII(part)
	}
	return strings.Join(parts, " ")
}

func capitalizeASCII(value string) string {
	switch len(value) {
	case 0:
		return ""
	case 1:
		return strings.ToUpper(value)
	default:
		lower := strings.ToLower(value)
		return strings.ToUpper(lower[:1]) + lower[1:]
	}
}

func infoSummaryKey(component, itemID, _ string, attrs []kv) string {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		if disc := attrValue(attrs, "disc_title"); disc != "" {
			itemID = "disc:" + disc
		} else if label := attrValue(attrs, "disc_label"); label != "" {
			itemID = "label:" + label
		} else if component != "" {
			itemID = component
		}
	}
	if itemID == "" {
		return ""
	}
	return itemID
}

func attrValue(attrs []kv, key string) string {
	for _, kv := range attrs {
		if kv.key == key {
			return attrString(kv.value)
		}
	}
	return ""
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

func attrString(v slog.Value) string {
	v = v.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			return err.Error()
		}
		return fmt.Sprint(v.Any())
	default:
		return formatValue(v)
	}
}

func formatValue(v slog.Value) string {
	v = v.Resolve()
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if needsQuotes(s) {
			return strconv.Quote(s)
		}
		return s
	case slog.KindBool:
		return strconv.FormatBool(v.Bool())
	case slog.KindInt64:
		return strconv.FormatInt(v.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(v.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.FormatFloat(v.Float64(), 'f', -1, 64)
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().UTC().Format(time.RFC3339)
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			msg := err.Error()
			if needsQuotes(msg) {
				return strconv.Quote(msg)
			}
			return msg
		}
		s := fmt.Sprint(v.Any())
		if needsQuotes(s) {
			return strconv.Quote(s)
		}
		return s
	default:
		s := v.String()
		if needsQuotes(s) {
			return strconv.Quote(s)
		}
		return s
	}
}

func needsQuotes(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r <= ' ' || r == '=' || r == '"' {
			return true
		}
	}
	return false
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
