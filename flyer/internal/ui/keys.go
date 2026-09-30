package ui

import "charm.land/bubbles/v2/key"

// keyMap defines all keyboard bindings for the application.
type keyMap struct {
	// Global
	Quit       key.Binding
	Help       key.Binding
	CycleTheme key.Binding
	Escape     key.Binding

	// View switching
	ViewQueue      key.Binding
	ViewDaemonLogs key.Binding
	ViewProblems   key.Binding

	// Data refresh
	Refresh key.Binding

	// Inspector
	Inspect     key.Binding
	InspectLogs key.Binding
	Tab         key.Binding
	ShiftTab    key.Binding
	Tab1        key.Binding
	Tab2        key.Binding
	Tab3        key.Binding
	Tab4        key.Binding
	Tab5        key.Binding

	// Queue actions
	CycleFilter   key.Binding
	Filter        key.Binding
	ToggleDetails key.Binding

	// Navigation
	Up           key.Binding
	Down         key.Binding
	Top          key.Binding
	Bottom       key.Binding
	PageUp       key.Binding
	PageDown     key.Binding
	HalfPageUp   key.Binding
	HalfPageDown key.Binding

	// Logs actions
	ToggleFollow key.Binding
	Search       key.Binding
	NextMatch    key.Binding
	PrevMatch    key.Binding
	LogFilters   key.Binding

	// Search/input
	Confirm key.Binding
}

// DefaultKeyMap returns the default key bindings.
func DefaultKeyMap() keyMap {
	return keyMap{
		// Global
		Quit: key.NewBinding(
			key.WithKeys("q", "Q", "ctrl+c"),
			key.WithHelp("q", "Quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("h", "?"),
			key.WithHelp("h/?", "Help"),
		),
		CycleTheme: key.NewBinding(
			key.WithKeys("T"),
			key.WithHelp("T", "Cycle theme"),
		),
		Escape: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "Back"),
		),

		// View switching
		ViewQueue: key.NewBinding(
			key.WithKeys("d", "D"),
			key.WithHelp("d", "Queue (dashboard)"),
		),
		ViewDaemonLogs: key.NewBinding(
			key.WithKeys("l", "L"),
			key.WithHelp("l", "Daemon logs"),
		),
		ViewProblems: key.NewBinding(
			key.WithKeys("p", "P"),
			key.WithHelp("p", "Problems"),
		),

		// Data refresh
		Refresh: key.NewBinding(
			key.WithKeys("r", "R"),
			key.WithHelp("r", "Refresh now"),
		),

		// Inspector
		Inspect: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("Enter", "Inspect item"),
		),
		InspectLogs: key.NewBinding(
			key.WithKeys("i", "I"),
			key.WithHelp("i", "Item logs"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("Tab", "Next tab"),
		),
		ShiftTab: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("Shift+Tab", "Previous tab"),
		),
		Tab1: key.NewBinding(
			key.WithKeys("1"),
			key.WithHelp("1", "Overview"),
		),
		Tab2: key.NewBinding(
			key.WithKeys("2"),
			key.WithHelp("2", "Episodes / File"),
		),
		Tab3: key.NewBinding(
			key.WithKeys("3"),
			key.WithHelp("3", "Problems"),
		),
		Tab4: key.NewBinding(
			key.WithKeys("4"),
			key.WithHelp("4", "Logs"),
		),
		Tab5: key.NewBinding(
			key.WithKeys("5"),
			key.WithHelp("5", "Events"),
		),

		// Queue actions
		CycleFilter: key.NewBinding(
			key.WithKeys("f", "F"),
			key.WithHelp("f", "Cycle filter"),
		),
		Filter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "Filter by title"),
		),
		// "t" only: "T" cycles the theme (documented case exception).
		// Episodes tab: file details; logs: structured fields.
		ToggleDetails: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "Toggle details"),
		),

		// Navigation
		Up: key.NewBinding(
			key.WithKeys("k", "up"),
			key.WithHelp("k/up", "Move up"),
		),
		Down: key.NewBinding(
			key.WithKeys("j", "down"),
			key.WithHelp("j/down", "Move down"),
		),
		Top: key.NewBinding(
			key.WithKeys("g", "home"),
			key.WithHelp("g", "Go to top"),
		),
		Bottom: key.NewBinding(
			key.WithKeys("G", "end"),
			key.WithHelp("G", "Go to bottom"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup"),
			key.WithHelp("pgup", "Page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown"),
			key.WithHelp("pgdown", "Page down"),
		),
		HalfPageUp: key.NewBinding(
			key.WithKeys("ctrl+u"),
			key.WithHelp("ctrl+u", "Half page up"),
		),
		HalfPageDown: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "Half page down"),
		),

		// Logs actions
		ToggleFollow: key.NewBinding(
			key.WithKeys(" "),
			key.WithHelp("Space", "Toggle follow"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "Search logs"),
		),
		NextMatch: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "Next match"),
		),
		PrevMatch: key.NewBinding(
			key.WithKeys("N"),
			key.WithHelp("N", "Previous match"),
		),
		LogFilters: key.NewBinding(
			key.WithKeys("f", "F"),
			key.WithHelp("f", "Log filters"),
		),

		// Search/input
		Confirm: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "Confirm"),
		),
	}
}

// HelpSection groups related key bindings for display in the help modal.
type HelpSection struct {
	Title    string
	Bindings []key.Binding
}

// HelpSections returns structured help data for the help modal. Paired
// bindings share one row so the two-column layout fits an 80x24 terminal.
func (k keyMap) HelpSections() []HelpSection {
	row := func(keys, desc string) key.Binding { return key.NewBinding(key.WithHelp(keys, desc)) }
	return []HelpSection{
		{
			Title:    "Views",
			Bindings: []key.Binding{k.ViewQueue, k.ViewDaemonLogs, k.ViewProblems, k.Escape},
		},
		{
			Title: "Inspector",
			Bindings: []key.Binding{
				k.Inspect, k.InspectLogs,
				row("1-5", "Jump to tab"),
				row("Tab/S-Tab", "Next/prev tab"),
				row("t", "Toggle file details"),
			},
		},
		{
			Title: "Navigation",
			Bindings: []key.Binding{
				row("j/k", "Down/up"),
				row("g/G", "Top/bottom"),
				row("^d/^u", "Half page down/up"),
				row("PgDn/PgUp", "Page down/up"),
			},
		},
		{
			Title:    "Queue",
			Bindings: []key.Binding{k.Filter, k.CycleFilter},
		},
		{
			Title:    "Logs",
			Bindings: []key.Binding{k.ToggleFollow, k.Search, row("n/N", "Next/prev match"), k.LogFilters, row("t", "Toggle fields")},
		},
		{
			Title:    "General",
			Bindings: []key.Binding{k.Refresh, k.CycleTheme, k.Help, k.Quit},
		},
		{
			Title:    "Task strip",
			Bindings: []key.Binding{row("✓ ◉ ○ ✗", "done/run/wait/fail")},
		},
	}
}
