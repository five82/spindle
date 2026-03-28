package httpapi

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeTempLog(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write temp log: %v", err)
	}
}

func TestParseJSONLogLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantOK  bool
		checkFn func(t *testing.T, e LogEntry)
	}{
		{
			name:   "standard slog fields",
			line:   `{"time":"2026-03-28T10:00:00Z","level":"INFO","msg":"hello world"}`,
			wantOK: true,
			checkFn: func(t *testing.T, e LogEntry) {
				if e.Time != "2026-03-28T10:00:00Z" {
					t.Errorf("Time = %q, want %q", e.Time, "2026-03-28T10:00:00Z")
				}
				if e.Level != "INFO" {
					t.Errorf("Level = %q, want %q", e.Level, "INFO")
				}
				if e.Msg != "hello world" {
					t.Errorf("Msg = %q, want %q", e.Msg, "hello world")
				}
			},
		},
		{
			name:   "known attribute keys",
			line:   `{"time":"2026-03-28T10:00:00Z","level":"INFO","msg":"test","component":"encoder","stage":"encoding","item_id":42,"lane":"main","request":"abc123"}`,
			wantOK: true,
			checkFn: func(t *testing.T, e LogEntry) {
				if e.Component != "encoder" {
					t.Errorf("Component = %q, want %q", e.Component, "encoder")
				}
				if e.Stage != "encoding" {
					t.Errorf("Stage = %q, want %q", e.Stage, "encoding")
				}
				if e.ItemID != 42 {
					t.Errorf("ItemID = %d, want %d", e.ItemID, 42)
				}
				if e.Lane != "main" {
					t.Errorf("Lane = %q, want %q", e.Lane, "main")
				}
				if e.Request != "abc123" {
					t.Errorf("Request = %q, want %q", e.Request, "abc123")
				}
			},
		},
		{
			name:   "unknown fields go to Fields map",
			line:   `{"time":"2026-03-28T10:00:00Z","level":"INFO","msg":"test","event_type":"stage_complete","path":"/tmp/foo"}`,
			wantOK: true,
			checkFn: func(t *testing.T, e LogEntry) {
				if e.Fields["event_type"] != "stage_complete" {
					t.Errorf("Fields[event_type] = %q, want %q", e.Fields["event_type"], "stage_complete")
				}
				if e.Fields["path"] != "/tmp/foo" {
					t.Errorf("Fields[path] = %q, want %q", e.Fields["path"], "/tmp/foo")
				}
			},
		},
		{
			name:   "malformed JSON",
			line:   `not json at all`,
			wantOK: false,
		},
		{
			name:   "empty line",
			line:   ``,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, ok := parseJSONLogLine([]byte(tt.line))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && tt.checkFn != nil {
				tt.checkFn(t, e)
			}
		})
	}
}

func TestHydrateFromDir(t *testing.T) {
	dir := t.TempDir()

	// Write two log files (lexicographic order = chronological).
	writeTempLog(t, dir, "spindle-20260328T100000.000Z.log",
		`{"time":"2026-03-28T10:00:00Z","level":"INFO","msg":"first","item_id":1}`+"\n"+
			`{"time":"2026-03-28T10:00:01Z","level":"INFO","msg":"second","item_id":1}`+"\n",
	)
	writeTempLog(t, dir, "spindle-20260328T110000.000Z.log",
		`{"time":"2026-03-28T11:00:00Z","level":"INFO","msg":"third","item_id":2}`+"\n",
	)

	// Also write a daemon.log symlink -- should be ignored.
	_ = os.Symlink(
		filepath.Join(dir, "spindle-20260328T110000.000Z.log"),
		filepath.Join(dir, "daemon.log"),
	)
	// Write a non-matching file -- should be ignored.
	writeTempLog(t, dir, "other.txt", "not a log\n")

	buf := NewLogBuffer(100)
	if err := buf.HydrateFromDir(dir); err != nil {
		t.Fatalf("HydrateFromDir: %v", err)
	}

	entries, next := buf.Query(LogQueryOpts{Limit: 100})
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}

	// Verify chronological order and sequence numbers.
	if entries[0].Msg != "first" || entries[0].Seq != 1 {
		t.Errorf("entries[0]: msg=%q seq=%d, want msg=%q seq=%d", entries[0].Msg, entries[0].Seq, "first", 1)
	}
	if entries[1].Msg != "second" || entries[1].Seq != 2 {
		t.Errorf("entries[1]: msg=%q seq=%d, want msg=%q seq=%d", entries[1].Msg, entries[1].Seq, "second", 2)
	}
	if entries[2].Msg != "third" || entries[2].Seq != 3 {
		t.Errorf("entries[2]: msg=%q seq=%d, want msg=%q seq=%d", entries[2].Msg, entries[2].Seq, "third", 3)
	}

	// Next sequence should be ready for new entries.
	if next != 4 {
		t.Errorf("next = %d, want 4", next)
	}

	// Verify item_id filtering works on hydrated entries.
	filtered, _ := buf.Query(LogQueryOpts{ItemID: 2, Limit: 100})
	if len(filtered) != 1 || filtered[0].Msg != "third" {
		t.Errorf("item filter: got %d entries, want 1 with msg=%q", len(filtered), "third")
	}
}

func TestHydrateFromDir_OverCapacity(t *testing.T) {
	dir := t.TempDir()

	// Write more lines than buffer capacity.
	var lines string
	for i := 0; i < 15; i++ {
		lines += fmt.Sprintf(`{"time":"2026-03-28T10:%02d:00Z","level":"INFO","msg":"line%d"}`+"\n", i, i)
	}
	writeTempLog(t, dir, "spindle-20260328T100000.000Z.log", lines)

	buf := NewLogBuffer(10) // capacity 10
	if err := buf.HydrateFromDir(dir); err != nil {
		t.Fatalf("HydrateFromDir: %v", err)
	}

	entries, _ := buf.Query(LogQueryOpts{Limit: 100})
	if len(entries) != 10 {
		t.Fatalf("got %d entries, want 10", len(entries))
	}

	// Should have the last 10 entries (line5 through line14).
	if entries[0].Msg != "line5" {
		t.Errorf("oldest entry: msg=%q, want %q", entries[0].Msg, "line5")
	}
	if entries[9].Msg != "line14" {
		t.Errorf("newest entry: msg=%q, want %q", entries[9].Msg, "line14")
	}
}

func TestHydrateFromDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	buf := NewLogBuffer(100)
	if err := buf.HydrateFromDir(dir); err != nil {
		t.Fatalf("HydrateFromDir on empty dir: %v", err)
	}

	entries, _ := buf.Query(LogQueryOpts{Limit: 100})
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(entries))
	}
}
