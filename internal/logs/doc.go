// Package logs provides file tailing and offset helpers shared by the CLI and
// daemon diagnostics.
//
// It streams log files with bounded memory usage, supports negative offsets for
// "tail last N lines" operations, and powers follow-mode updates for
// `spindle show --follow`. Callers supply context deadlines so background
// polling shuts down cleanly when the CLI exits.
//
// Use this package whenever you need consistent log viewing semantics instead
// of re-implementing ad-hoc tail logic.
package logs
