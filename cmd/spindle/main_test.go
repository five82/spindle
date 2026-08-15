package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
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

func TestLongRunningActionCommandsHaveQuietFlag(t *testing.T) {
	commands := []*cobra.Command{
		newRipCmd(),
		newEncodeCmd(),
		newGensubtitleCmd(),
		newCacheRipCmd(),
	}
	for _, cmd := range commands {
		flag := cmd.Flags().Lookup("quiet")
		if flag == nil {
			t.Errorf("%s has no --quiet flag", cmd.CommandPath())
			continue
		}
		if flag.Shorthand != "q" {
			t.Errorf("%s --quiet shorthand = %q, want q", cmd.CommandPath(), flag.Shorthand)
		}
	}
}

func TestQuietRaisesCommandLogLevelToWarn(t *testing.T) {
	oldQuiet, oldLevel := flagQuiet, flagLogLevel
	t.Cleanup(func() {
		flagQuiet, flagLogLevel = oldQuiet, oldLevel
	})
	flagQuiet, flagLogLevel = true, "debug"

	logger := buildLogger()
	if logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("quiet logger unexpectedly enables INFO")
	}
	if !logger.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("quiet logger suppresses WARN")
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
