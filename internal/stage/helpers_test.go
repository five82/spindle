package stage

import (
	"testing"
)

func TestParseRipSpec_Valid(t *testing.T) {
	raw := `{"fingerprint":"fp-1","titles":[{"id":1,"name":"T1","duration":100,"title_hash":"h1"}]}`
	env, err := ParseRipSpec(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Fingerprint != "fp-1" {
		t.Fatalf("unexpected fingerprint: %q", env.Fingerprint)
	}
}

func TestParseRipSpec_Empty(t *testing.T) {
	env, err := ParseRipSpec("")
	if err != nil {
		t.Fatalf("unexpected error for empty input: %v", err)
	}
	if env.Fingerprint != "" {
		t.Fatalf("expected empty envelope for empty input")
	}
}

func TestParseRipSpec_Invalid(t *testing.T) {
	_, err := ParseRipSpec("{invalid json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
