package services_test

import (
	"errors"
	"strings"
	"testing"

	"spindle/internal/queue"
	"spindle/internal/services"
)

func TestWrapAndUnwrap(t *testing.T) {
	base := errors.New("boom")
	err := services.Wrap(services.ErrorExternalTool, "encoding", "mux", "failed", base)
	if err == nil {
		t.Fatal("expected error")
	}
	se, ok := err.(*services.ServiceError)
	if !ok {
		t.Fatalf("expected ServiceError, got %T", err)
	}
	if se.Code != services.ErrorExternalTool {
		t.Fatalf("unexpected code %q", se.Code)
	}
	if se.FailureStatus() != queue.StatusFailed {
		t.Fatalf("expected failed outcome, got %s", se.FailureStatus())
	}
	if !errors.Is(err, base) {
		t.Fatalf("expected errors.Is to match wrapped error")
	}
	if got := err.Error(); !strings.Contains(got, "[external_tool]") || !strings.Contains(got, "cause=boom") {
		t.Fatalf("unexpected error message: %s", got)
	}
}

func TestWithMessageClonesError(t *testing.T) {
	base := services.Wrap(services.ErrorValidation, "organizer", "prepare", "original", nil)
	updated := services.WithMessage(base, "updated message")
	if got := updated.Error(); !strings.Contains(got, "updated message") || !strings.Contains(got, "stage=organizer") {
		t.Fatalf("unexpected message: %s", got)
	}
	if base.Error() == updated.Error() {
		t.Fatal("expected messages to differ")
	}
}

func TestWithHintAndOutcome(t *testing.T) {
	err := services.Wrap(services.ErrorValidation, "identifier", "validate", "validation failed", nil)
	withHint := services.WithHint(err, "fix metadata")
	se, ok := withHint.(*services.ServiceError)
	if !ok {
		t.Fatalf("expected ServiceError, got %T", withHint)
	}
	if se.Hint != "fix metadata" {
		t.Fatalf("expected hint to be set, got %q", se.Hint)
	}
	if se.FailureStatus() != queue.StatusReview {
		t.Fatalf("expected review outcome, got %s", se.FailureStatus())
	}
	withOutcome := services.WithOutcome(withHint, queue.StatusFailed)
	se2, ok := withOutcome.(*services.ServiceError)
	if !ok {
		t.Fatalf("expected ServiceError, got %T", withOutcome)
	}
	if se2.FailureStatus() != queue.StatusFailed {
		t.Fatalf("expected overridden outcome failed, got %s", se2.FailureStatus())
	}
	if !strings.Contains(se2.Error(), "hint=fix metadata") {
		t.Fatalf("expected hint in error string, got %s", se2.Error())
	}
}
