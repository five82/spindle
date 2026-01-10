package services_test

import (
	"errors"
	"strings"
	"testing"

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

func TestErrorKindClassification(t *testing.T) {
	// Error classification is used for logging and diagnostics
	validationErr := services.Wrap(services.ErrValidation, "identifier", "prepare", "invalid", nil)
	details := services.Details(validationErr)
	if details.Kind != services.ErrorKindValidation {
		t.Fatalf("expected validation kind, got %s", details.Kind)
	}

	transientErr := services.Wrap(services.ErrTransient, "encoding", "copy", "copy failed", errors.New("io"))
	details = services.Details(transientErr)
	if details.Kind != services.ErrorKindTransient {
		t.Fatalf("expected transient kind, got %s", details.Kind)
	}
}
