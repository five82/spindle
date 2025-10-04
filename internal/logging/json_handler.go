package logging

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
)

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
