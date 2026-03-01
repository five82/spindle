package stage

import (
	"context"
	"log/slog"

	"spindle/internal/queue"
)

// Handler describes the contract the workflow manager needs from each stage.
type Handler interface {
	Prepare(context.Context, *queue.Item) error
	Execute(context.Context, *queue.Item) error
	HealthCheck(context.Context) Health
}

// LoggerAware is implemented by stages that accept a per-item logger.
type LoggerAware interface {
	SetLogger(*slog.Logger)
}
