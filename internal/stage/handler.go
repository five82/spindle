package stage

import (
	"context"

	"spindle/internal/queue"
)

// Handler describes the contract the workflow manager needs from each stage.
type Handler interface {
	Prepare(context.Context, *queue.Item) error
	Execute(context.Context, *queue.Item) error
	HealthCheck(context.Context) Health
}
