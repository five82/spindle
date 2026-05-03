package stage

import "context"

// Handler is the interface that all pipeline stages must implement.
// Workflow and one-shot stage execution create the Session so handlers share a
// single boundary for queue-visible state: RipSpec persistence, progress,
// review state, and active episode bookkeeping.
type Handler interface {
	Run(ctx context.Context, sess *Session) error
}
