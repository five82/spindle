package spindle

import (
	"testing"
	"time"
)

func TestEncodingHelpers(t *testing.T) {
	if (*EncodingStatus)(nil).ETADuration() != 0 {
		t.Fatalf("ETADuration on nil should be 0")
	}
	if (&EncodingStatus{ETASeconds: 2.5}).ETADuration() != 2500*time.Millisecond {
		t.Fatalf("ETADuration mismatch")
	}
}

func TestParseTimeLayouts(t *testing.T) {
	rfc := "2025-12-13T10:11:12Z"
	got := parseTime(rfc)
	if got.IsZero() {
		t.Fatalf("parseTime should parse RFC3339")
	}
	if got.Year() != 2025 || got.Month() != time.December || got.Day() != 13 {
		t.Fatalf("parseTime = %v, want 2025-12-13", got)
	}

	nano := "2025-12-13T10:11:12.123456789Z"
	if parseTime(nano).IsZero() {
		t.Fatalf("parseTime should parse RFC3339Nano")
	}

	// Spindle no longer emits the bare "2006-01-02 15:04:05" layout; it is
	// not a supported input and should not parse.
	if got := parseTime("2025-12-13 10:11:12"); !got.IsZero() {
		t.Fatalf("parseTime(%q) = %v, want zero time (unsupported layout)", "2025-12-13 10:11:12", got)
	}

	if !parseTime("").IsZero() {
		t.Fatalf("parseTime(\"\") should be zero time")
	}
}

func TestEpisodeSnapshot_UsesAPIFieldsWhenPresent(t *testing.T) {
	item := QueueItem{
		Episodes: []EpisodeStatus{
			{Key: "a", RippedPath: "/r"},
			{Key: "b", EncodedPath: "/e"},
		},
	}
	episodes, totals := item.EpisodeSnapshot()
	if len(episodes) != 2 {
		t.Fatalf("episodes len = %d, want 2", len(episodes))
	}
	if totals.Planned != 2 || totals.Ripped != 1 || totals.Encoded != 1 || totals.Subtitled != 0 || totals.Final != 0 {
		t.Fatalf("totals = %#v, want planned=2 ripped=1 encoded=1 subtitled=0 final=0", totals)
	}
	episodes[0].Key = "mutated"
	episodes2, _ := item.EpisodeSnapshot()
	if episodes2[0].Key != "a" {
		t.Fatalf("EpisodeSnapshot should return a copy of episodes")
	}
}

func TestEpisodeSnapshot_EmptyWhenNoEpisodes(t *testing.T) {
	episodes, totals := QueueItem{}.EpisodeSnapshot()
	if episodes != nil {
		t.Fatalf("episodes = %#v, want nil", episodes)
	}
	if totals != (EpisodeTotals{}) {
		t.Fatalf("totals = %#v, want zero value", totals)
	}
}

func TestTaskHelpers(t *testing.T) {
	item := QueueItem{Tasks: []Task{
		{Type: "ripping", State: "done"},
		{Type: "encoding", State: "running", ActiveAssetKey: "S01E02"},
		{Type: "subtitling", State: "pending"},
	}}

	if running := item.RunningTasks(); len(running) != 1 || running[0].Type != "encoding" {
		t.Fatalf("RunningTasks() = %#v, want single encoding task", running)
	}

	primary := item.PrimaryTask()
	if primary == nil || primary.Type != "encoding" {
		t.Fatalf("PrimaryTask() = %#v, want running encoding task", primary)
	}

	keys := item.ActiveAssetKeys()
	if !keys["s01e02"] {
		t.Fatalf("ActiveAssetKeys() = %#v, want lowercase s01e02", keys)
	}

	failed := QueueItem{Tasks: []Task{{Type: "encoding", State: "failed"}}}
	if ft := failed.FailedTask(); ft == nil || ft.Type != "encoding" {
		t.Fatalf("FailedTask() = %#v, want failed encoding task", ft)
	}
}

func TestIsTerminal(t *testing.T) {
	if (QueueItem{Stage: "encoding"}).IsTerminal() {
		t.Fatalf("encoding should not be terminal")
	}
	if !(QueueItem{Stage: "completed"}).IsTerminal() {
		t.Fatalf("completed should be terminal")
	}
	if !(QueueItem{Stage: "failed"}).IsTerminal() {
		t.Fatalf("failed should be terminal")
	}
}
