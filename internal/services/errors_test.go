package services_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/five82/spindle/internal/services"
)

func TestErrDegraded(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
		asMatch bool
	}{
		{
			name:    "bare",
			err:     &services.ErrDegraded{Msg: "tmdb returned no results"},
			wantMsg: "tmdb returned no results",
			asMatch: true,
		},
		{
			name:    "with cause",
			err:     &services.ErrDegraded{Msg: "cache write failed", Cause: errors.New("disk full")},
			wantMsg: "cache write failed: disk full",
			asMatch: true,
		},
		{
			name:    "wrapped with fmt.Errorf",
			err:     fmt.Errorf("metadata stage: %w", &services.ErrDegraded{Msg: "no results"}),
			wantMsg: "metadata stage: no results",
			asMatch: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tt.wantMsg)
			}
			var target *services.ErrDegraded
			if got := errors.As(tt.err, &target); got != tt.asMatch {
				t.Errorf("errors.As = %v, want %v", got, tt.asMatch)
			}
		})
	}
}

func TestErrDegradedUnwrap(t *testing.T) {
	inner := errors.New("underlying")
	err := &services.ErrDegraded{Msg: "degraded", Cause: inner}
	if !errors.Is(err, inner) {
		t.Error("errors.Is should find the wrapped cause")
	}
	bare := &services.ErrDegraded{Msg: "no cause"}
	if bare.Unwrap() != nil {
		t.Error("Unwrap should return nil when no cause")
	}
}

func TestErrValidation(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
		asMatch bool
	}{
		{
			name:    "bare",
			err:     &services.ErrValidation{Msg: "invalid rip spec"},
			wantMsg: "invalid rip spec",
			asMatch: true,
		},
		{
			name:    "with cause",
			err:     &services.ErrValidation{Msg: "bad schema", Cause: errors.New("missing field")},
			wantMsg: "bad schema: missing field",
			asMatch: true,
		},
		{
			name:    "wrapped with fmt.Errorf",
			err:     fmt.Errorf("parse: %w", &services.ErrValidation{Msg: "bad input"}),
			wantMsg: "parse: bad input",
			asMatch: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tt.wantMsg)
			}
			var target *services.ErrValidation
			if got := errors.As(tt.err, &target); got != tt.asMatch {
				t.Errorf("errors.As = %v, want %v", got, tt.asMatch)
			}
		})
	}
}

func TestErrTimeout(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
		asMatch bool
	}{
		{
			name:    "bare",
			err:     &services.ErrTimeout{Msg: "rip timed out"},
			wantMsg: "rip timed out",
			asMatch: true,
		},
		{
			name:    "wrapping context deadline",
			err:     &services.ErrTimeout{Msg: "makemkv rip", Cause: context.DeadlineExceeded},
			wantMsg: "makemkv rip: context deadline exceeded",
			asMatch: true,
		},
		{
			name:    "wrapped with fmt.Errorf",
			err:     fmt.Errorf("stage: %w", &services.ErrTimeout{Msg: "timed out"}),
			wantMsg: "stage: timed out",
			asMatch: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tt.wantMsg)
			}
			var target *services.ErrTimeout
			if got := errors.As(tt.err, &target); got != tt.asMatch {
				t.Errorf("errors.As = %v, want %v", got, tt.asMatch)
			}
		})
	}
}

func TestErrTimeoutUnwrapDeadline(t *testing.T) {
	err := &services.ErrTimeout{Msg: "rip", Cause: context.DeadlineExceeded}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Error("errors.Is should find context.DeadlineExceeded through Unwrap")
	}
}

func TestErrExternalTool(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
		asMatch bool
	}{
		{
			name:    "basic",
			err:     &services.ErrExternalTool{Tool: "makemkv", ExitCode: 1},
			wantMsg: "makemkv failed (exit 1)",
			asMatch: true,
		},
		{
			name:    "with message",
			err:     &services.ErrExternalTool{Tool: "ffmpeg", ExitCode: 2, Msg: "segment error"},
			wantMsg: "ffmpeg failed (exit 2): segment error",
			asMatch: true,
		},
		{
			name: "with message and cause",
			err: &services.ErrExternalTool{
				Tool: "makemkv", ExitCode: 1, Msg: "disc read error",
				Cause: errors.New("I/O timeout"),
			},
			wantMsg: "makemkv failed (exit 1): disc read error: I/O timeout",
			asMatch: true,
		},
		{
			name:    "wrapped with fmt.Errorf",
			err:     fmt.Errorf("rip stage: %w", &services.ErrExternalTool{Tool: "makemkv", ExitCode: 1}),
			wantMsg: "rip stage: makemkv failed (exit 1)",
			asMatch: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tt.wantMsg)
			}
			var target *services.ErrExternalTool
			if got := errors.As(tt.err, &target); got != tt.asMatch {
				t.Errorf("errors.As = %v, want %v", got, tt.asMatch)
			}
		})
	}
}

func TestErrExternalToolFields(t *testing.T) {
	inner := errors.New("broken pipe")
	err := &services.ErrExternalTool{Tool: "ffmpeg", ExitCode: 137, Cause: inner}
	var target *services.ErrExternalTool
	if !errors.As(err, &target) {
		t.Fatal("errors.As should match")
	}
	if target.Tool != "ffmpeg" {
		t.Errorf("Tool = %q, want %q", target.Tool, "ffmpeg")
	}
	if target.ExitCode != 137 {
		t.Errorf("ExitCode = %d, want %d", target.ExitCode, 137)
	}
	if !errors.Is(err, inner) {
		t.Error("errors.Is should find the wrapped cause")
	}
}

func TestNonDegradedDoesNotMatchDegraded(t *testing.T) {
	errs := []error{
		&services.ErrValidation{Msg: "bad"},
		&services.ErrTimeout{Msg: "slow"},
		&services.ErrExternalTool{Tool: "x", ExitCode: 1},
		errors.New("plain error"),
	}
	for _, err := range errs {
		var target *services.ErrDegraded
		if errors.As(err, &target) {
			t.Errorf("errors.As(ErrDegraded) should not match %T", err)
		}
	}
}
