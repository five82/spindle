package llm

import (
	"errors"
	"testing"
)

func TestRetryableErrorPreservesCause(t *testing.T) {
	cause := errors.New("temporary failure")
	wrapped := &retryableError{err: cause}
	if !errors.Is(wrapped, cause) || wrapped.Error() != cause.Error() || !isRetryable(wrapped) {
		t.Fatalf("lost cause: %v", wrapped)
	}
}
