package stage

import (
	"context"

	"github.com/five82/spindle/internal/queue"
)

// Handler is the interface that all pipeline stages must implement.
type Handler interface {
	Run(ctx context.Context, item *queue.Item) error
}
