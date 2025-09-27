package services

import (
	"fmt"
	"strings"

	"spindle/internal/queue"
)

// ErrorCode categorizes operational failures for consistent daemon handling.
type ErrorCode string

const (
	ErrorExternalTool  ErrorCode = "external_tool"
	ErrorValidation    ErrorCode = "validation"
	ErrorConfiguration ErrorCode = "configuration"
	ErrorNotFound      ErrorCode = "not_found"
	ErrorTimeout       ErrorCode = "timeout"
	ErrorTransient     ErrorCode = "transient"
)

// ServiceError wraps an error with structured context for workflow handling.
type ServiceError struct {
	Code      ErrorCode
	Stage     string
	Operation string
	Err       error
	Message   string
	Hint      string
	Outcome   queue.Status
}

// Error implements the error interface.
func (e *ServiceError) Error() string {
	parts := make([]string, 0, 5)
	if e.Code != "" {
		parts = append(parts, fmt.Sprintf("[%s]", e.Code))
	}
	base := strings.TrimSpace(e.Message)
	if base == "" && e.Err != nil {
		base = strings.TrimSpace(e.Err.Error())
	}
	if base != "" {
		parts = append(parts, base)
	}
	contextParts := make([]string, 0, 3)
	if e.Stage != "" {
		contextParts = append(contextParts, fmt.Sprintf("stage=%s", e.Stage))
	}
	if e.Operation != "" {
		contextParts = append(contextParts, fmt.Sprintf("op=%s", e.Operation))
	}
	if e.Err != nil && (base == "" || !strings.Contains(base, e.Err.Error())) {
		contextParts = append(contextParts, fmt.Sprintf("cause=%s", strings.TrimSpace(e.Err.Error())))
	}
	if len(contextParts) > 0 {
		parts = append(parts, strings.Join(contextParts, ", "))
	}
	if strings.TrimSpace(e.Hint) != "" {
		parts = append(parts, fmt.Sprintf("hint=%s", strings.TrimSpace(e.Hint)))
	}
	if len(parts) == 0 {
		return "unknown error"
	}
	return strings.Join(parts, " | ")
}

// Unwrap exposes the underlying error.
func (e *ServiceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Wrap constructs a ServiceError from the provided arguments.
func Wrap(code ErrorCode, stage, operation, message string, err error) error {
	se := &ServiceError{
		Code:      code,
		Stage:     stage,
		Operation: operation,
		Message:   message,
		Err:       err,
		Outcome:   defaultOutcome(code),
	}
	return se
}

// WithMessage returns a copy of the error with a new message.
func WithMessage(err error, message string) error {
	if err == nil {
		return nil
	}
	if se, ok := err.(*ServiceError); ok {
		dup := *se
		dup.Message = message
		return &dup
	}
	return &ServiceError{Message: message, Err: err}
}

// Outcome returns the queue status the workflow should apply for this failure.
func (e *ServiceError) FailureStatus() queue.Status {
	if e == nil {
		return queue.StatusFailed
	}
	if e.Outcome == "" {
		return defaultOutcome(e.Code)
	}
	return e.Outcome
}

// WithHint annotates the error with remediation guidance.
func WithHint(err error, hint string) error {
	if err == nil || strings.TrimSpace(hint) == "" {
		return err
	}
	if se, ok := err.(*ServiceError); ok {
		dup := *se
		dup.Hint = hint
		return &dup
	}
	return &ServiceError{Message: err.Error(), Err: err, Hint: hint}
}

// WithOutcome overrides the default queue status outcome for the error.
func WithOutcome(err error, status queue.Status) error {
	if err == nil || status == "" {
		return err
	}
	if se, ok := err.(*ServiceError); ok {
		dup := *se
		dup.Outcome = status
		return &dup
	}
	return &ServiceError{Outcome: status, Message: err.Error()}
}

func defaultOutcome(code ErrorCode) queue.Status {
	switch code {
	case ErrorValidation, ErrorConfiguration, ErrorNotFound:
		return queue.StatusReview
	default:
		return queue.StatusFailed
	}
}
