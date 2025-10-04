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
}

func newPrettyHandler(w io.Writer, lvl *slog.LevelVar, addSource bool) slog.Handler {
	return &prettyHandler{writer: w, level: lvl, addSource: addSource}
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

	var component string
	filtered := kvs[:0]
	for _, kv := range kvs {
		if kv.key == "component" {
			if component == "" {
				component = attrString(kv.value)
			}
			continue
		}
		filtered = append(filtered, kv)
	}
	kvs = filtered

	var buf bytes.Buffer
	buf.Grow(128 + len(kvs)*24)

	buf.WriteString(timestamp.UTC().Format(time.RFC3339))
	buf.WriteByte(' ')
	buf.WriteString(levelLabel(record.Level))
	buf.WriteByte(' ')

	if component != "" {
		buf.WriteString(component)
		buf.WriteString(": ")
	}

	if msg := strings.TrimSpace(record.Message); msg != "" {
		buf.WriteString(msg)
	} else {
		buf.WriteString("(no message)")
	}

	if h.addSource {
		if src := record.Source(); src != nil {
			buf.WriteString(" [")
			buf.WriteString(filepath.Base(src.File))
			buf.WriteByte(':')
			buf.WriteString(strconv.Itoa(src.Line))
			buf.WriteByte(']')
		}
	}

	for _, kv := range kvs {
		if kv.key == "" {
			continue
		}
		buf.WriteByte(' ')
		buf.WriteString(kv.key)
		buf.WriteByte('=')
		buf.WriteString(formatValue(kv.value))
	}

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.writer.Write(buf.Bytes())
	return err
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
