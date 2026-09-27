package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/five82/flyer/internal/spindle"
)

func inspectorModelFor(item spindle.QueueItem) Model {
	m := New(Options{ThemeName: "slate"})
	m.width = 120
	m.snapshot.Queue = []spindle.QueueItem{item}
	m.inspectedID = item.ID
	return m
}

func TestInspectorTabBar_ProblemsMarkerFollowsAttention(t *testing.T) {
	m := inspectorModelFor(spindle.QueueItem{ID: 9, Stage: "failed", ErrorMessage: "boom"})
	got := stripANSI(m.renderInspectorTabBar(m.theme.BandStyles()))
	if !strings.Contains(got, "Problems ⚠") {
		t.Fatalf("problem item must mark the Problems tab, got %q", got)
	}

	m = inspectorModelFor(spindle.QueueItem{ID: 9, Stage: "encoding"})
	got = stripANSI(m.renderInspectorTabBar(m.theme.BandStyles()))
	if strings.Contains(got, "⚠") {
		t.Fatalf("healthy item must not mark the Problems tab, got %q", got)
	}
}

func TestInspectorItemLine_IdentitySegments(t *testing.T) {
	item := spindle.QueueItem{
		ID:           9,
		Stage:        "encoding",
		DisplayTitle: "The Abyss",
		Metadata:     json.RawMessage(`{"year":"1989"}`),
		Source:       &spindle.SourceTitle{TitleID: 2, DurationSeconds: 171 * 60},
	}
	m := inspectorModelFor(item)
	got := stripANSI(m.renderInspectorItemLine(m.theme.BandStyles()))
	for _, want := range []string{"The Abyss", "(1989)", "2h 51m", "ID #9"} {
		if !strings.Contains(got, want) {
			t.Fatalf("item line missing %q, got %q", want, got)
		}
	}
	if !strings.HasPrefix(got, "Queue › ID #9 › The Abyss") {
		t.Fatalf("item line must lead with the item number and title, got %q", got)
	}
}

func TestInspectorItemLine_TVDiscNumber(t *testing.T) {
	item := spindle.QueueItem{
		ID:           9,
		Stage:        "encoding",
		DisplayTitle: "The Simpsons Season 06",
		DiscNumber:   2,
		Metadata:     json.RawMessage(`{"year":"1989","media_type":"tv"}`),
	}
	m := inspectorModelFor(item)
	got := stripANSI(m.renderInspectorItemLine(m.theme.BandStyles()))
	normalized := strings.Join(strings.Fields(got), " ")
	if !strings.Contains(normalized, "ID #9 › The Simpsons Season 06 (1989) disc 2") {
		t.Fatalf("item line missing numbered TV disc identity, got %q", got)
	}
}

func TestInspectorItemLine_NoDuplicateYear(t *testing.T) {
	item := spindle.QueueItem{
		ID:           9,
		Stage:        "encoding",
		DisplayTitle: "The Abyss (1989)",
		Metadata:     json.RawMessage(`{"year":"1989"}`),
	}
	m := inspectorModelFor(item)
	got := stripANSI(m.renderInspectorItemLine(m.theme.BandStyles()))
	if strings.Count(got, "1989") != 1 {
		t.Fatalf("year must not repeat when the title carries it, got %q", got)
	}
}

func TestInspectorItemLine_ShedsRuntimeFirstWhenNarrow(t *testing.T) {
	item := spindle.QueueItem{
		ID:           9,
		Stage:        "encoding",
		DisplayTitle: "A Fairly Long Movie Title For Shedding",
		Metadata:     json.RawMessage(`{"year":"1989"}`),
		Source:       &spindle.SourceTitle{TitleID: 2, DurationSeconds: 171 * 60},
	}
	m := inspectorModelFor(item)
	wide := stripANSI(m.renderInspectorItemLine(m.theme.BandStyles()))
	if !strings.Contains(wide, "2h 51m") {
		t.Fatalf("wide item line missing runtime, got %q", wide)
	}

	m.width = len("Queue › ID #9 › " + item.DisplayTitle + "  (1989)  ENCODING")
	narrow := stripANSI(m.renderInspectorItemLine(m.theme.BandStyles()))
	if strings.Contains(narrow, "2h 51m") {
		t.Fatalf("narrow item line must shed runtime first, got %q", narrow)
	}
	if !strings.Contains(narrow, item.DisplayTitle) {
		t.Fatalf("narrow item line must keep the title, got %q", narrow)
	}
}

func TestCommandBarEpisodeHintFollowsEpisodicItems(t *testing.T) {
	movie := inspectorModelFor(spindle.QueueItem{
		ID:       9,
		Stage:    "completed",
		Episodes: []spindle.EpisodeStatus{{Key: "main"}},
		Metadata: json.RawMessage(`{"media_type":"movie"}`),
	})
	movie.inspecting = true
	if got := stripANSI(movie.renderCommandBar()); strings.Contains(got, "Episodes") {
		t.Fatalf("movie inspector must not advertise the episode toggle, got %q", got)
	}

	tv := inspectorModelFor(spindle.QueueItem{
		ID:       9,
		Stage:    "ripping",
		Metadata: json.RawMessage(`{"media_type":"tv"}`),
	})
	tv.inspecting = true
	if got := stripANSI(tv.renderCommandBar()); !strings.Contains(got, "Episodes") {
		t.Fatalf("TV inspector must advertise the episode toggle, got %q", got)
	}
}

func TestStatusChips_StoppedItem(t *testing.T) {
	m := New(Options{ThemeName: "slate"})
	got := stripANSI(m.renderStatusChips(spindle.QueueItem{ID: 7, Stage: "encoding", UserStopped: true}, m.theme.Styles()))
	if !strings.Contains(got, "STOPPED") {
		t.Fatalf("user-stopped item missing STOPPED chip, got %q", got)
	}
}
