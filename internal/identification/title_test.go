package identification

import "testing"

func TestDeriveTitleFromPath(t *testing.T) {
	title := deriveTitle("/movies/Some_Sample-Title (2021).iso")
	if title != "Some Sample Title 2021" {
		t.Fatalf("unexpected title %q", title)
	}
}

func TestDeriveTitleUnknownWhenEmpty(t *testing.T) {
	if got := deriveTitle(""); got != "Unknown Disc" {
		t.Fatalf("expected fallback, got %q", got)
	}
}
