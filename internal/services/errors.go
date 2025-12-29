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

// ErrorKind captures the taxonomy of service errors.
type ErrorKind string

const (
	ErrorKindExternal      ErrorKind = "external"
	ErrorKindValidation    ErrorKind = "validation"
	ErrorKindConfiguration ErrorKind = "configuration"
	ErrorKindNotFound      ErrorKind = "not_found"
	ErrorKindTimeout       ErrorKind = "timeout"
	ErrorKindTransient     ErrorKind = "transient"
)

// ServiceError provides structured error context for workflow failures.
type ServiceError struct {
	Marker     error
	Kind       ErrorKind
	Stage      string
	Operation  string
	Message    string
	Code       string
	Hint       string
	DetailPath string
	Cause      error
}

func (e *ServiceError) Error() string {
	if e == nil {
		return ""
	}
	detail := buildDetail(e.Stage, e.Operation, e.Message)
	if detail == "" {
		detail = "service failure"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", detail, e.Cause)
	}
	return detail
}

func (e *ServiceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *ServiceError) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	if e.Marker != nil && errors.Is(e.Marker, target) {
		return true
	}
	return errors.Is(e.Cause, target)
}

// ErrorDetails exposes a snapshot of a ServiceError for structured logging.
type ErrorDetails struct {
	Kind       ErrorKind
	Stage      string
	Operation  string
	Message    string
	Code       string
	Hint       string
	DetailPath string
	Cause      error
}

// Details extracts structured error information when available.
func Details(err error) ErrorDetails {
	var svcErr *ServiceError
	if errors.As(err, &svcErr) && svcErr != nil {
		return ErrorDetails{
			Kind:       svcErr.Kind,
			Stage:      svcErr.Stage,
			Operation:  svcErr.Operation,
			Message:    strings.TrimSpace(svcErr.Message),
			Code:       strings.TrimSpace(svcErr.Code),
			Hint:       strings.TrimSpace(svcErr.Hint),
			DetailPath: strings.TrimSpace(svcErr.DetailPath),
			Cause:      svcErr.Cause,
		}
	}
	return ErrorDetails{
		Kind:    ErrorKindTransient,
		Message: strings.TrimSpace(errorMessage(err)),
		Cause:   err,
	}
}

// Wrap builds an error message that includes stage context while tagging it with
// the provided marker for later status classification. The marker should be one
// of the exported sentinel errors above.
func Wrap(marker error, stage, operation, message string, err error) error {
	return wrapWithOptions(marker, stage, operation, message, err)
}

// WrapDetail attaches a detail path to the resulting error.
func WrapDetail(marker error, stage, operation, message string, err error, detailPath string) error {
	return wrapWithOptions(marker, stage, operation, message, err, WithDetailPath(detailPath))
}

// WrapHint attaches a stable error code and hint to the resulting error.
func WrapHint(marker error, stage, operation, message, code, hint string, err error) error {
	return wrapWithOptions(marker, stage, operation, message, err, WithCode(code), WithHint(hint))
}

// Wrap builds an error message that includes stage context while tagging it with
// the provided marker for later status classification. The marker should be one
// of the exported sentinel errors above.
type wrapOption func(*ServiceError)

func WithDetailPath(path string) wrapOption {
	return func(err *ServiceError) {
		if err != nil {
			err.DetailPath = strings.TrimSpace(path)
		}
	}
}

func WithCode(code string) wrapOption {
	return func(err *ServiceError) {
		if err != nil {
			err.Code = strings.TrimSpace(code)
		}
	}
}

func WithHint(hint string) wrapOption {
	return func(err *ServiceError) {
		if err != nil {
			err.Hint = strings.TrimSpace(hint)
		}
	}
}

func wrapWithOptions(marker error, stage, operation, message string, err error, opts ...wrapOption) error {
	if marker == nil {
		marker = ErrTransient
	}
	kind, code := classifyMarker(marker)
	serviceErr := &ServiceError{
		Marker:    marker,
		Kind:      kind,
		Stage:     strings.TrimSpace(stage),
		Operation: strings.TrimSpace(operation),
		Message:   strings.TrimSpace(message),
		Code:      code,
		Cause:     err,
	}
	if err != nil {
		var nested *ServiceError
		if errors.As(err, &nested) && nested != nil {
			if strings.TrimSpace(serviceErr.DetailPath) == "" {
				serviceErr.DetailPath = nested.DetailPath
			}
			if strings.TrimSpace(serviceErr.Code) == "" {
				serviceErr.Code = nested.Code
			}
			if strings.TrimSpace(serviceErr.Hint) == "" {
				serviceErr.Hint = nested.Hint
			}
		}
	}
	for _, opt := range opts {
		opt(serviceErr)
	}
	if serviceErr.Hint == "" && serviceErr.DetailPath != "" {
		serviceErr.Hint = "See error_detail_path for tool output"
	}
	return serviceErr
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

func classifyMarker(marker error) (ErrorKind, string) {
	switch {
	case errors.Is(marker, ErrExternalTool):
		return ErrorKindExternal, "E_EXTERNAL"
	case errors.Is(marker, ErrValidation):
		return ErrorKindValidation, "E_VALIDATION"
	case errors.Is(marker, ErrConfiguration):
		return ErrorKindConfiguration, "E_CONFIGURATION"
	case errors.Is(marker, ErrNotFound):
		return ErrorKindNotFound, "E_NOT_FOUND"
	case errors.Is(marker, ErrTimeout):
		return ErrorKindTimeout, "E_TIMEOUT"
	case errors.Is(marker, ErrTransient):
		return ErrorKindTransient, "E_TRANSIENT"
	default:
		return ErrorKindTransient, "E_TRANSIENT"
	}
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
