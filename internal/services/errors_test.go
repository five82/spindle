package services_test

import (
	"errors"
	"strings"
	"testing"

	"spindle/internal/queue"
	"spindle/internal/services"
)

func TestWrapIncludesContext(t *testing.T) {
	base := errors.New("boom")
	err := services.Wrap(services.ErrExternalTool, "encoding", "mux", "failed", base)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, services.ErrExternalTool) {
		t.Fatalf("expected marker to be retained, got %v", err)
	}
	if !errors.Is(err, base) {
		t.Fatalf("expected wrapped error to contain base error, got %v", err)
	}
	msg := err.Error()
	for _, fragment := range []string{"encoding", "mux", "failed"} {
		if !strings.Contains(msg, fragment) {
			t.Fatalf("expected %q in error string %q", fragment, msg)
		}
	}
}

func TestFailureStatusMapping(t *testing.T) {
	validationErr := services.Wrap(services.ErrValidation, "identifier", "prepare", "invalid", nil)
	if status := services.FailureStatus(validationErr); status != queue.StatusReview {
		t.Fatalf("expected review for validation error, got %s", status)
	}

	transientErr := services.Wrap(services.ErrTransient, "encoding", "copy", "copy failed", errors.New("io"))
	if status := services.FailureStatus(transientErr); status != queue.StatusFailed {
		t.Fatalf("expected failed for transient error, got %s", status)
	}

	if status := services.FailureStatus(nil); status != queue.StatusFailed {
		t.Fatalf("expected failed for nil error, got %s", status)
	}
}
