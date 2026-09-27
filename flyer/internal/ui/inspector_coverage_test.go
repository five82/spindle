package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func TestInspectorNavigationAndMissingItem(t *testing.T) {
	m := newAppTestModel(t)
	m, _ = updateApp(t, m, tea.WindowSizeMsg{Width: 90, Height: 20})
	m.selectedRow = 1
	m, _ = updateApp(t, m, appKey("enter"))
	if !m.inspecting || m.inspectedID != 2 || m.returnView != ViewQueue {
		t.Fatalf("inspector did not open on selected item: %+v", m)
	}
	m.filterMode = FilterFailed // inspected item must still resolve outside current filter
	if got := m.getInspectedItem(); got == nil || got.ID != 2 {
		t.Fatalf("filtered item = %+v", got)
	}
	m, _ = updateApp(t, m, appKey("2"))
	if m.inspectorTab != tabEpisodes {
		t.Fatalf("tab 2 = %d", m.inspectorTab)
	}
	m, _ = updateApp(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.inspectorTab != tabProblems {
		t.Fatalf("tab next = %d", m.inspectorTab)
	}
	m, _ = updateApp(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.inspectorTab != tabLogs || m.logState.mode != logSourceItem {
		t.Fatalf("logs tab state = %d/%d", m.inspectorTab, m.logState.mode)
	}
	m, _ = updateApp(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.inspectorTab != tabEvents {
		t.Fatalf("events tab = %d", m.inspectorTab)
	}
	m, _ = updateApp(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.inspectorTab != tabOverview {
		t.Fatalf("tab wrap = %d", m.inspectorTab)
	}
	m, _ = updateApp(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.inspectorTab != tabEvents {
		t.Fatalf("reverse tab wrap = %d", m.inspectorTab)
	}
	m, _ = updateApp(t, m, appKey("5"))
	if m.inspectorTab != tabEvents {
		t.Fatalf("tab 5 = %d", m.inspectorTab)
	}
	m, _ = updateApp(t, m, appKey("3"))
	if m.inspectorTab != tabProblems {
		t.Fatalf("tab 3 = %d", m.inspectorTab)
	}
	m, _ = updateApp(t, m, appKey("1"))
	if m.inspectorTab != tabOverview {
		t.Fatalf("tab 1 = %d", m.inspectorTab)
	}
	m, _ = updateApp(t, m, appKey("esc"))
	if m.inspecting || m.currentView != ViewQueue {
		t.Fatal("escape must restore queue")
	}
	m.inspectedID = 999
	if got := m.getInspectedItem(); got != nil {
		t.Fatalf("missing item = %+v", got)
	}
	m.inspecting = true
	m.updateInspectorViewport()
	if got := stripANSI(m.inspectorViewport.View()); !strings.Contains(got, "Item no longer in queue") {
		t.Fatalf("missing item view = %q", got)
	}
	if got := stripANSI(m.renderInspectorItemLine(m.theme.BandStyles())); !strings.Contains(got, "ID #999") || !strings.Contains(got, "gone") {
		t.Fatalf("missing item band = %q", got)
	}
}

func TestInspectorRendersTabsAndEpisodeCollapse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	item := spindle.QueueItem{ID: 42, DisplayTitle: "Show", Stage: "failed", ErrorMessage: "disc error", Episodes: []spindle.EpisodeStatus{{Key: "s01e01", Season: 1, Episode: 1, Title: "Pilot"}}}
	m := inspectorModelFor(item)
	m.height = 22
	m.inspecting = true
	m.initInspectorViewport()
	for _, tc := range []struct {
		tab  inspectorTab
		want string
	}{
		{tabOverview, "Overview"}, {tabEpisodes, "Episodes"}, {tabProblems, "Problems"}, {tabLogs, "Logs"}, {tabEvents, "Events"},
	} {
		m.inspectorTab = tc.tab
		m.updateInspectorViewport()
		got := stripANSI(m.renderInspector())
		if !strings.Contains(got, tc.want) || !strings.Contains(got, "Show") {
			t.Errorf("tab %v output = %q", tc.tab, got)
		}
	}
	m.inspectorTab = tabEpisodes
	m.updateInspectorViewport()
	if got := stripANSI(m.inspectorViewport.View()); !strings.Contains(got, "Pilot") {
		t.Fatalf("episodes tab = %q", got)
	}
	before := m.isEpisodesCollapsed(item, item.Episodes, spindle.EpisodeTotals{Planned: 1})
	m.toggleInspectedEpisodes()
	if got := m.detailState.episodeCollapsed[item.ID]; got == before {
		t.Fatalf("toggle collapse = %v, was %v", got, before)
	}
	m.inspectedID = 999
	m.toggleInspectedEpisodes() // missing item is a no-op
	if len(m.detailState.episodeCollapsed) != 1 {
		t.Fatal("missing item changed collapse state")
	}
	if got := stripANSI(m.renderEpisodesTab(spindle.QueueItem{})); !strings.Contains(got, "No episodes") {
		t.Fatalf("empty episodes tab = %q", got)
	}
}

func TestInspectorFromProblemsAndScrollingKeys(t *testing.T) {
	m := newAppTestModel(t)
	m, _ = updateApp(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m.currentView = ViewProblems
	opened, _ := m.openInspector(tabOverview)
	m = opened.(Model)
	if !m.inspecting || m.returnView != ViewProblems || m.inspectedID != 1 {
		t.Fatalf("problems inspector = %+v", m)
	}
	for _, keyMsg := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: tea.KeyUp}, {Code: tea.KeyHome}, {Code: tea.KeyEnd},
		{Code: tea.KeyPgDown}, {Code: tea.KeyPgUp}, appKey("j"), appKey("k"),
	} {
		updated, _ := m.handleInspectorKey(keyMsg)
		m = updated.(Model)
		if !m.inspecting {
			t.Fatal("scroll key closed inspector")
		}
	}
	m.closeInspector()
	if m.currentView != ViewProblems || m.inspecting {
		t.Fatal("close did not restore problems view")
	}
	m.snapshot.Queue = nil
	opened, cmd := m.openInspector(tabOverview)
	if opened.(Model).inspecting || cmd != nil {
		t.Fatal("inspector opened with no selected item")
	}
}
