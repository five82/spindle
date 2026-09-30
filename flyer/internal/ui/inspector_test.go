package ui

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/flyer/internal/spindle"
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
	if !strings.Contains(got, "3 Problems (1) ⚠") {
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
	for _, want := range []string{"The Abyss", "(1989)", "2h 51m", "#9"} {
		if !strings.Contains(got, want) {
			t.Fatalf("item line missing %q, got %q", want, got)
		}
	}
	if !strings.HasPrefix(got, "Queue › #9 The Abyss") {
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
	if !strings.Contains(normalized, "#9 The Simpsons Season 06 (1989) disc 2") {
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

	m.width = len("Queue › #9 " + item.DisplayTitle + "  (1989)   ENCODING ")
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
	movie.inspectorTab = tabEpisodes
	if got := stripANSI(movie.renderCommandBar()); !strings.Contains(got, "Details") {
		t.Fatalf("movie inspector must not advertise the episode toggle, got %q", got)
	}

	tv := inspectorModelFor(spindle.QueueItem{
		ID:       9,
		Stage:    "ripping",
		Metadata: json.RawMessage(`{"media_type":"tv"}`),
	})
	tv.inspecting = true
	tv.inspectorTab = tabEpisodes
	if got := stripANSI(tv.renderCommandBar()); !strings.Contains(got, "Details") {
		t.Fatalf("TV inspector must advertise the episode toggle, got %q", got)
	}
	tv.inspectorTab = tabOverview
	if got := stripANSI(tv.renderCommandBar()); strings.Contains(got, "Details") {
		t.Fatalf("Overview has no file details to toggle, got %q", got)
	}
}

func TestStatusChips_StoppedItem(t *testing.T) {
	m := New(Options{ThemeName: "slate"})
	got := stripANSI(m.renderStatusChips(spindle.QueueItem{ID: 7, Stage: "encoding", UserStopped: true}, m.theme.Styles()))
	if !strings.Contains(got, "STOPPED") {
		t.Fatalf("user-stopped item missing STOPPED chip, got %q", got)
	}
}

// The header clock dates a fresh snapshot; the item band only discloses the
// fetch age once the snapshot is stale.
func TestInspectorItemLineDisclosesOnlyStaleFetch(t *testing.T) {
	m := inspectorModelFor(spindle.QueueItem{ID: 9, Stage: "encoding"})
	m.snapshot.LastUpdated = time.Now().Add(-3 * time.Minute)
	if got := stripANSI(m.renderInspectorItemLine(m.theme.BandStyles())); strings.Contains(got, "fetched") {
		t.Fatalf("fresh snapshot must not repeat the fetch age, got %q", got)
	}
	m.snapshot.LastError = errors.New("connection refused")
	if got := stripANSI(m.renderInspectorItemLine(m.theme.BandStyles())); !strings.Contains(got, "stale: fetched 3m ago") {
		t.Fatalf("stale snapshot must disclose its age, got %q", got)
	}
}

func TestInspectorTabBarCountsAndMarksActiveWithoutColor(t *testing.T) {
	m := inspectorModelFor(spindle.QueueItem{ID: 9, Stage: "encoding", Metadata: json.RawMessage(`{"media_type":"tv"}`),
		Episodes: []spindle.EpisodeStatus{{Key: "a"}, {Key: "b"}, {Key: "c"}}})
	m.inspectorTab = tabEpisodes
	raw := m.renderInspectorTabBar(m.theme.BandStyles())
	if got := stripANSI(raw); !strings.Contains(got, " 2 Episodes (3) ") || strings.Contains(got, "Problems (") {
		t.Fatalf("tab counts = %q", got)
	}
	// Reverse video (SGR 7) marks the active tab for monochrome terminals.
	if !regexp.MustCompile(`\x1b\[(\d+;)*7(;[\d;]*)?m 2 Episodes`).MatchString(raw) || strings.Count(raw, ";7;") != 1 {
		t.Fatalf("only the active tab must be reverse video: %q", raw)
	}
}

// The tab bar names the active tab; the panel border and section headers
// must not stack a second and third title line under it.
func TestInspectorDoesNotRepeatTitles(t *testing.T) {
	m := inspectorModelFor(spindle.QueueItem{ID: 9, Stage: "encoding", Tasks: []spindle.Task{{Type: "encoding", State: "running"}}})
	m.height = 24
	m.inspecting = true
	m.updateInspectorViewport()
	got := stripANSI(m.renderInspector())
	if !strings.Contains(got, "\n┌"+strings.Repeat("─", m.width-2)+"┐\n") {
		t.Fatalf("untitled panel border must be solid:\n%s", got)
	}
	if strings.Count(got, "Overview") != 1 {
		t.Fatalf("Overview must appear only in the tab bar:\n%s", got)
	}
	if strings.Contains(got, "── Pipeline") || !strings.Contains(got, "│ Pipeline ") {
		t.Fatalf("section header must be a plain label:\n%s", got)
	}
}
