package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/five82/flyer/internal/spindle"
)

// stageInfo is the ONE place a spindle stage name maps to display
// treatment. Order and dependency edges come from the daemon's pipeline
// template (/api/status); this catalog only styles the names it knows.
// Unknown stage names render neutrally with their raw name -- flyer must
// never need a code change when spindle renames or adds a stage.
type stageInfo struct {
	label     string // present tense (running)
	doneLabel string // past tense (done)
	role      string // color role: accent, info, warning, success, danger
	totals    string // per-episode throughput source ("" = item-level)
}

var stageCatalog = map[string]stageInfo{
	"identification":         {"Identifying", "Identified", "info", ""},
	"ripping":                {"Ripping", "Ripped", "accent", "ripped"},
	"episode_identification": {"Ep. Matching", "Ep. Matched", "info", ""},
	"encoding":               {"Encoding", "Encoded", "warning", "encoded"},
	"analysis":               {"Analyzing", "Analyzed", "info", ""},
	"subtitling":             {"Subtitling", "Subtitled", "info", "subtitle_generated"},
	"apply":                  {"Applying", "Applied", "info", ""},
	"organizing":             {"Organizing", "Organized", "success", "final"},
	// Terminal item stages (not tasks).
	"completed": {"Completed", "Completed", "success", ""},
	"failed":    {"Failed", "Failed", "danger", ""},
}

// stageDisplay returns display info for a stage, neutral for unknown names.
func stageDisplay(stage string) stageInfo {
	key := strings.ToLower(strings.TrimSpace(stage))
	if info, ok := stageCatalog[key]; ok {
		return info
	}
	return stageInfo{label: key, doneLabel: key, role: ""}
}

// roleStyle resolves a catalog color role against the current theme styles.
func roleStyle(role string, styles Styles) lipgloss.Style {
	switch role {
	case "accent":
		return styles.AccentText
	case "info":
		return styles.InfoText
	case "warning":
		return styles.WarningText
	case "success":
		return styles.SuccessText
	case "danger":
		return styles.DangerText
	default:
		return styles.Text
	}
}

// Task state glyphs for strips and boards.
func taskStateGlyph(state string) string {
	switch state {
	case "done":
		return "✓"
	case "running":
		return "◉"
	case "failed":
		return "✗"
	default:
		return "○"
	}
}

// resourceOrder derives the resource display order from the pipeline
// template (first appearance wins), so the NOW band follows the daemon's
// own declaration instead of a hardcoded list.
func resourceOrder(pipeline []spindle.PipelineStage, resources map[string]spindle.ResourceStatus) []string {
	var order []string
	seen := make(map[string]bool)
	for _, st := range pipeline {
		for _, claim := range st.Claims {
			if _, ok := resources[claim]; ok && !seen[claim] {
				order = append(order, claim)
				seen[claim] = true
			}
		}
	}
	// Any resources the template didn't mention still show, after the rest.
	for name := range resources {
		if !seen[name] {
			order = append(order, name)
			seen[name] = true
		}
	}
	return order
}

// resourceLabel renders a resource name for the NOW band.
// Mechanical rules only: strip the "encode_" prefix, uppercase short names.
func resourceLabel(name string) string {
	name = strings.TrimPrefix(name, "encode_")
	if len(name) <= 3 {
		return strings.ToUpper(name)
	}
	if name == "drive" {
		return "Drive"
	}
	return name
}

// itemDisplayStage returns the stage name that best describes the item now:
// terminal stage, else the primary task's type, else the coarse stage.
func itemDisplayStage(item spindle.QueueItem) string {
	if item.IsTerminal() {
		return strings.ToLower(item.Stage)
	}
	if t := item.PrimaryTask(); t != nil {
		return t.Type
	}
	return strings.ToLower(item.Stage)
}

// itemSortRank orders queue rows: review first, then failed, then items
// with running tasks, then other active items, completed last.
func itemSortRank(item spindle.QueueItem) int {
	switch {
	case item.NeedsReview:
		return 0
	case strings.EqualFold(item.Stage, "failed"):
		return 1
	case len(item.RunningTasks()) > 0:
		return 2
	case !item.IsTerminal():
		return 3
	default:
		return 4
	}
}
