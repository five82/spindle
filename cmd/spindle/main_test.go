package main

import (
	"strings"
	"testing"
	"time"
)

func TestTruncateIsRuneSafe(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("truncate short = %q", got)
	}
	got := truncate("héllo wörld exceeds", 10)
	if !strings.HasSuffix(got, "..") {
		t.Fatalf("truncate missing ellipsis: %q", got)
	}
	// Must remain valid UTF-8 (no split multi-byte sequences).
	if strings.ContainsRune(got, '�') {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
	// Multi-byte title whose byte length exceeds maxLen but rune length does not.
	multibyte := strings.Repeat("é", 9) // 18 bytes, 9 runes
	if got := truncate(multibyte, 10); got != multibyte {
		t.Fatalf("truncate cut a string within its rune budget: %q", got)
	}
}

func TestRelativeAge(t *testing.T) {
	recent := time.Now().Add(-90 * time.Minute).UTC().Format(time.RFC3339Nano)
	got := relativeAge(recent)
	if !strings.HasSuffix(got, " ago") {
		t.Fatalf("relativeAge(%q) = %q, want ...ago", recent, got)
	}
	// SQLite raw layout fallback.
	sqlite := time.Now().Add(-2 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	if got := relativeAge(sqlite); !strings.HasSuffix(got, " ago") {
		t.Fatalf("relativeAge sqlite layout = %q, want ...ago", got)
	}
	// Unparseable values pass through verbatim.
	if got := relativeAge("not-a-time"); got != "not-a-time" {
		t.Fatalf("relativeAge passthrough = %q", got)
	}
}
