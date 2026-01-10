package queue

// ErrorClassifier allows errors to declare their classification for logging and diagnostics.
// This interface is retained for error categorization even though all failures now result
// in StatusFailed.
type ErrorClassifier interface {
	// ErrorKind returns a string classification of the error.
	// Common kinds: "validation", "configuration", "not_found", "external", "timeout"
	ErrorKind() string
}

// FailureStatus returns StatusFailed for any error.
// All queue failures are retryable; error classification is used only for
// logging and diagnostics, not status routing.
func FailureStatus(_ error) Status {
	return StatusFailed
}
