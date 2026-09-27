package ui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/flyer/internal/config"
	"github.com/five82/spindle/flyer/internal/spindle"
	"github.com/five82/spindle/flyer/internal/state"
)

func TestConnectingHeaderStates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := New(Options{PrefsPath: filepath.Join(home, "prefs.toml")})
	m.width = 140
	if got := stripANSI(m.renderHeader()); !strings.Contains(got, "Connecting to Spindle") {
		t.Fatalf("initial header = %q", got)
	}
	m.snapshot.LastError = errors.New("connection refused")
	m.lastUpdated = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	m.config = &config.Config{StateDir: filepath.Join(home, strings.Repeat("long", 20))}
	got := stripANSI(m.renderHeader())
	for _, want := range []string{"OFFLINE", "Retrying", "04:05:06", "logs ", "..."} {
		if !strings.Contains(got, want) {
			t.Errorf("header %q missing %q", got, want)
		}
	}
}

func TestConnectionErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{errors.New("connection refused"), "OFFLINE"},
		{errors.New("no such host"), "HOST NOT FOUND"},
		{errors.New("request timeout"), "TIMEOUT"},
		{errors.New("permission denied"), "ERROR"},
	} {
		if got := classifyConnectionError(tc.err); got != tc.want {
			t.Errorf("classifyConnectionError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestHeaderTimeAndHealthFormats(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := New(Options{ThemeName: "slate", PrefsPath: filepath.Join(home, "prefs.toml")})
	if got := m.formatTimestamp(); got != "" {
		t.Fatalf("zero timestamp = %q", got)
	}
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{
		{30 * time.Second, ""}, {5 * time.Minute, "(5m)"},
		{3 * time.Hour, "(3h)"}, {25 * time.Hour, ":"},
	} {
		m.lastUpdated = time.Now().Add(-tc.age)
		got := m.formatTimestamp()
		if !strings.Contains(got, tc.want) {
			t.Errorf("age %v: timestamp = %q, want %q", tc.age, got, tc.want)
		}
		if tc.age > 24*time.Hour && (strings.Contains(got, "(") || len(got) != 8) {
			t.Errorf("old timestamp = %q", got)
		}
	}
	styles := m.theme.Styles()
	if got := m.formatHealthWarning(false, styles); got != "" {
		t.Fatalf("healthy = %q", got)
	}
	m.snapshot.Status.Dependencies = []spindle.DependencyStatus{
		{Name: "drive", Available: false, Detail: "disconnected"},
		{Name: "encoder", Available: false},
		{Name: "optional", Available: true},
	}
	for _, compact := range []bool{true, false} {
		got := stripANSI(m.formatHealthWarning(compact, styles))
		if !strings.Contains(got, "drive") || !strings.Contains(got, "disconnected") || !strings.Contains(got, "+1 more") || strings.Contains(got, "optional") {
			t.Fatalf("health warning = %q", got)
		}
	}
}

func TestHeaderCountsErrorsAndDropping(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := New(Options{ThemeName: "slate", PrefsPath: filepath.Join(home, "prefs.toml")})
	m.width = 160
	m.snapshot = state.Snapshot{HasStatus: true, Status: spindle.StatusResponse{
		Running: false, Workflow: spindle.WorkflowStatus{LastError: "  disk full  "},
		Dependencies: []spindle.DependencyStatus{{Name: "drive", Available: false}},
	}, Queue: []spindle.QueueItem{
		{Stage: "FAILED", NeedsReview: true, Tasks: []spindle.Task{{State: "running"}}},
		{Stage: "encoding", NeedsReview: true},
	}}
	m.snapshot.LastError = errors.New("API unavailable")
	m.errorMsg = "refresh failed"
	if n := m.countProcessingItems(); n != 1 {
		t.Fatalf("processing items = %d", n)
	}
	styles := m.theme.Styles()
	for _, tc := range []struct {
		compact bool
		want    string
	}{{false, "Failed: 1"}, {true, "F: 1"}} {
		got := stripANSI(m.buildProblemCountsPart(tc.compact, 1, 2, styles))
		if !strings.Contains(got, tc.want) || !strings.Contains(got, "2") {
			t.Errorf("counts = %q", got)
		}
	}
	if got := m.buildProblemCountsPart(false, 0, 0, styles); got != "" {
		t.Fatalf("no problems = %q", got)
	}
	parts := m.buildErrorParts(true, styles)
	if len(parts) != 3 || !strings.Contains(stripANSI(strings.Join(parts, " ")), "API unavailable") {
		t.Fatalf("error parts = %v", parts)
	}
	got := stripANSI(m.renderHeader())
	for _, want := range []string{"OFF", "Failed:", "HEALTH", "WORKFLOW", "ERROR", "refresh failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("header %q missing %q", got, want)
		}
	}
	m.snapshot.LastError = nil
	m.width = 30
	got = stripANSI(m.renderHeader())
	if !strings.Contains(got, "flyer") || !strings.Contains(got, "OFF") || strings.Contains(got, "Queue:") {
		t.Fatalf("narrow header did not preserve essential parts: %q", got)
	}
}

func TestHeaderTruncationAndPriority(t *testing.T) {
	for _, tc := range []struct {
		input string
		max   int
		want  string
	}{
		{"abcdef", 0, ""}, {"abc", 4, "abc"}, {"abcdef", 2, "ab"}, {"abcdef", 5, "ab..."},
	} {
		if got := truncate(tc.input, tc.max); got != tc.want {
			t.Errorf("truncate(%q, %d) = %q", tc.input, tc.max, got)
		}
	}
	for _, tc := range []struct {
		input string
		max   int
		want  string
	}{
		{"abcdef", 0, ""}, {"abc", 4, "abc"}, {"abcdef", 4, "abcd"}, {"abcdefghij", 8, "ab...hij"},
	} {
		if got := truncateMiddle(tc.input, tc.max); got != tc.want {
			t.Errorf("truncateMiddle(%q, %d) = %q, want %q", tc.input, tc.max, got, tc.want)
		}
	}
	if maxLen(true, 80, 40) != 40 || maxLen(false, 80, 40) != 80 {
		t.Fatal("maxLen selection")
	}
	fill := GetTheme("slate").Styles().Band
	parts := []headerPart{{"essential", 0}, {"low", 3}, {"high", 1}}
	if got := stripANSI(joinHeaderParts(parts, 17, fill)); got != "essential  high" {
		t.Fatalf("priority drop = %q", got)
	}
	if got := stripANSI(joinHeaderParts(parts[:1], 2, fill)); got != "essential" {
		t.Fatalf("rank-zero drop = %q", got)
	}
}
