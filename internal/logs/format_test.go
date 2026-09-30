package logs

import (
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDurationAndCountsFormatting(t *testing.T) {
	l := slog.Default()
	if Default(nil) != l || Default(slog.New(slog.NewTextHandler(os.Stdout, nil))) == nil {
		t.Fatal("default logger")
	}
	duration := time.Minute + 1234*time.Millisecond
	text := FormatDuration(duration)
	if parsed, err := time.ParseDuration(text); text != "1m1.234s" || err != nil || parsed != duration {
		t.Fatalf("round trip %q: %v", text, err)
	}
	if got := FormatCounts(nil); got != "none" {
		t.Fatal(got)
	}
	if got := FormatCounts(map[string]int{"z": 2, "a": 1}); got != "a=1,z=2" {
		t.Fatal(got)
	}
}

func TestTailMissingAndDefaultLimit(t *testing.T) {
	if _, err := Tail(filepath.Join(t.TempDir(), "missing"), 1); err == nil {
		t.Fatal("missing log accepted")
	}
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 3)), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := Tail(path, 0)
	if err != nil || !reflect.DeepEqual(lines, []string{"line", "line", "line"}) {
		t.Fatalf("tail: %v %v", lines, err)
	}
}
