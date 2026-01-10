package logging

import (
	"context"
	"log/slog"
)

// FieldSessionID is the standardized structured logging key for diagnostic session identifiers.
const FieldSessionID = "session_id"

// sessionIDHandler wraps another handler to inject a session_id attribute into all records.
type sessionIDHandler struct {
	base      slog.Handler
	sessionID string
}

func newSessionIDHandler(base slog.Handler, sessionID string) slog.Handler {
	if base == nil {
		return NoopHandler{}
	}
	return &sessionIDHandler{
		base:      base,
		sessionID: sessionID,
	}
}

func (h *sessionIDHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *sessionIDHandler) Handle(ctx context.Context, record slog.Record) error {
	record.AddAttrs(slog.String(FieldSessionID, h.sessionID))
	return h.base.Handle(ctx, record)
}

func (h *sessionIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &sessionIDHandler{
		base:      h.base.WithAttrs(attrs),
		sessionID: h.sessionID,
	}
}

func (h *sessionIDHandler) WithGroup(name string) slog.Handler {
	return &sessionIDHandler{
		base:      h.base.WithGroup(name),
		sessionID: h.sessionID,
	}
}
