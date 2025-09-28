package services

import (
	"errors"
	"fmt"
	"strings"

	"spindle/internal/queue"
)

var (
	ErrExternalTool  = errors.New("external tool error")
	ErrValidation    = errors.New("validation error")
	ErrConfiguration = errors.New("configuration error")
	ErrNotFound      = errors.New("not found")
	ErrTimeout       = errors.New("timeout")
	ErrTransient     = errors.New("transient failure")
)

// Wrap builds an error message that includes stage context while tagging it with
// the provided marker for later status classification. The marker should be one
// of the exported sentinel errors above.
func Wrap(marker error, stage, operation, message string, err error) error {
	detail := buildDetail(stage, operation, message)
	if marker == nil {
		marker = ErrTransient
	}
	if err != nil {
		return fmt.Errorf("%w: %s: %w", marker, detail, err)
	}
	return fmt.Errorf("%w: %s", marker, detail)
}

// FailureStatus maps a stage error to the queue status the workflow manager
// should persist after the stage fails.
func FailureStatus(err error) queue.Status {
	switch {
	case errors.Is(err, ErrValidation), errors.Is(err, ErrConfiguration), errors.Is(err, ErrNotFound):
		return queue.StatusReview
	default:
		return queue.StatusFailed
	}
}

func buildDetail(stage, operation, message string) string {
	parts := make([]string, 0, 3)
	if stage = strings.TrimSpace(stage); stage != "" {
		parts = append(parts, stage)
	}
	if operation = strings.TrimSpace(operation); operation != "" {
		parts = append(parts, operation)
	}
	if message = strings.TrimSpace(message); message != "" {
		parts = append(parts, message)
	}
	if len(parts) == 0 {
		return "service failure"
	}
	return strings.Join(parts, ": ")
}
