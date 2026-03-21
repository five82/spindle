package services

import "fmt"

// ErrDegraded indicates degraded behavior: warn and continue.
// This is the only error that does not fail a pipeline item.
// Detect via errors.As.
type ErrDegraded struct {
	Msg   string
	Cause error
}

func (e *ErrDegraded) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Cause)
	}
	return e.Msg
}

func (e *ErrDegraded) Unwrap() error { return e.Cause }

// ErrValidation indicates a validation failure (bad input, schema errors).
type ErrValidation struct {
	Msg   string
	Cause error
}

func (e *ErrValidation) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Cause)
	}
	return e.Msg
}

func (e *ErrValidation) Unwrap() error { return e.Cause }

// ErrTimeout indicates a timeout (context deadline exceeded, etc.).
type ErrTimeout struct {
	Msg   string
	Cause error
}

func (e *ErrTimeout) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Cause)
	}
	return e.Msg
}

func (e *ErrTimeout) Unwrap() error { return e.Cause }

// ErrExternalTool indicates an external tool failure (MakeMKV, FFmpeg, etc.).
type ErrExternalTool struct {
	Tool     string
	ExitCode int
	Msg      string
	Cause    error
}

func (e *ErrExternalTool) Error() string {
	base := fmt.Sprintf("%s failed (exit %d)", e.Tool, e.ExitCode)
	if e.Msg != "" {
		base = fmt.Sprintf("%s: %s", base, e.Msg)
	}
	if e.Cause != nil {
		base = fmt.Sprintf("%s: %v", base, e.Cause)
	}
	return base
}

func (e *ErrExternalTool) Unwrap() error { return e.Cause }
