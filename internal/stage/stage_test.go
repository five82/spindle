package stage_test

import (
	"strings"
	"testing"

	"github.com/five82/spindle/internal/stage"
)

func TestParseRipSpec_Valid(t *testing.T) {
	env, err := stage.ParseRipSpec(`{"version":1,"metadata":{"media_type":"movie","title":"Test"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Metadata.MediaType != "movie" {
		t.Fatalf("expected media_type movie, got %s", env.Metadata.MediaType)
	}
}

func TestParseRipSpec_Empty(t *testing.T) {
	_, err := stage.ParseRipSpec("")
	if err != nil {
		t.Fatalf("unexpected error for empty string: %v", err)
	}
}

func TestParseRipSpec_InvalidJSON(t *testing.T) {
	_, err := stage.ParseRipSpec("{bad json")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "invalid rip spec") {
		t.Fatalf("expected invalid rip spec error, got %T: %v", err, err)
	}
}
