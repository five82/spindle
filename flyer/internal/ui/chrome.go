package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// renderRule renders a full-width horizontal rule with an embedded title:
// ── Title ───────────────
// The title renders bold; the dashes use the theme border color.
func renderRule(title string, width int, styles Styles) string {
	line := styles.RuleText.Render("── ")
	if title != "" {
		line += styles.Text.Bold(true).Render(title) + styles.RuleText.Render(" ")
	}
	used := lipgloss.Width(line)
	if width > used {
		line += styles.RuleText.Render(strings.Repeat("─", width-used))
	}
	return line
}

// padBand pads a chrome band line with background-filled cells to the full
// terminal width, so the band reads as one solid strip.
func padBand(line string, width int, band lipgloss.Style) string {
	if n := width - lipgloss.Width(line); n > 0 {
		line += band.Render(strings.Repeat(" ", n))
	}
	return line
}

// panelInnerWidth returns the content width inside a Level 1 panel:
// terminal width minus the border columns and one space of padding per side.
func panelInnerWidth(width int) int {
	return max(width-4, 1)
}

// renderPanel wraps content in a Level 1 single-line bordered panel with the
// title embedded in the top border and an optional footer (scroll position,
// counts) right-aligned in the bottom border (guide elevation model):
//
//	┌── Title ────────┐
//	│ content         │
//	└──── 3-9 of 40 ──┘
//
// Content lines are padded to the interior width; callers size their content
// to panelInnerWidth(width).
func renderPanel(title, content, footer string, width int, styles Styles) string {
	inner := panelInnerWidth(width)

	var b strings.Builder
	b.WriteString(styles.RuleText.Render("┌── "))
	dashes := width - 5
	if title != "" {
		b.WriteString(styles.Text.Bold(true).Render(title))
		b.WriteString(styles.RuleText.Render(" "))
		dashes = width - 6 - lipgloss.Width(title)
	}
	b.WriteString(styles.RuleText.Render(strings.Repeat("─", max(dashes, 0)) + "┐"))
	b.WriteString("\n")

	edge := styles.RuleText.Render("│")
	for _, line := range strings.Split(content, "\n") {
		if n := inner - lipgloss.Width(line); n > 0 {
			line += strings.Repeat(" ", n)
		}
		b.WriteString(edge + " " + line + " " + edge)
		b.WriteString("\n")
	}

	if footer != "" && lipgloss.Width(footer)+7 <= width {
		fw := lipgloss.Width(footer)
		b.WriteString(styles.RuleText.Render("└" + strings.Repeat("─", width-5-fw) + " "))
		b.WriteString(styles.FaintText.Render(footer))
		b.WriteString(styles.RuleText.Render(" ─┘"))
	} else {
		b.WriteString(styles.RuleText.Render("└" + strings.Repeat("─", max(width-2, 0)) + "┘"))
	}
	return b.String()
}

// chip renders a status badge: theme background color text on a colored fill.
func chip(label, colorHex string, theme Theme) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Background)).
		Background(lipgloss.Color(colorHex)).
		Padding(0, 1).
		Render(label)
}

// eighthBlocks are the left-partial block characters used for smooth
// progress fills; index = filled eighths of the final cell.
var eighthBlocks = [8]string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

// progressBlocks renders a percentage as plain block characters with
// eighth-block sub-cell resolution. The filled and empty runs are returned
// separately so callers can style them independently.
func progressBlocks(percent float64, width int) (filled, empty string) {
	percent = clampPercent(percent)
	cells := float64(width) * percent / 100
	full := min(int(cells), width)
	partial := ""
	if full < width {
		if idx := int((cells - float64(full)) * 8); idx > 0 {
			partial = eighthBlocks[idx]
		}
	}
	filled = strings.Repeat("█", full) + partial
	empty = strings.Repeat("░", width-full-len([]rune(partial)))
	return filled, empty
}

// renderProgressBar renders a progress bar with the filled run in the given
// style (typically the task's stage role color) and the empty run faint.
func renderProgressBar(percent float64, width int, fill lipgloss.Style, styles Styles) string {
	filled, empty := progressBlocks(percent, width)
	return fill.Render(filled) + styles.FaintText.Render(empty)
}

// overlayCenter composites an overlay box centered over the backdrop. The
// backdrop is stripped of its own colors and re-rendered faint so it reads
// as a scrim behind the modal. Oversized overlays fall back to plain
// centered placement.
func overlayCenter(backdrop, overlay string, width, height int, styles Styles) string {
	overlayLines := strings.Split(overlay, "\n")
	ow := 0
	for _, l := range overlayLines {
		ow = max(ow, lipgloss.Width(l))
	}
	oh := len(overlayLines)
	if ow > width-2 || oh > height || width <= 0 || height <= 0 {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, overlay)
	}
	x := (width - ow) / 2
	y := (height - oh) / 2

	bgLines := strings.Split(ansi.Strip(backdrop), "\n")
	rows := make([]string, height)
	for row := range rows {
		var bg string
		if row < len(bgLines) {
			bg = bgLines[row]
		}
		if w := ansi.StringWidth(bg); w < width {
			bg += strings.Repeat(" ", width-w)
		}
		if row < y || row >= y+oh {
			rows[row] = styles.FaintText.Render(bg)
			continue
		}
		line := overlayLines[row-y]
		if pad := ow - lipgloss.Width(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		rows[row] = styles.FaintText.Render(ansi.Cut(bg, 0, x)) +
			line +
			styles.FaintText.Render(ansi.Cut(bg, x+ow, width))
	}
	return strings.Join(rows, "\n")
}

// wrapText word-wraps s to the given width and returns the resulting lines.
// Words longer than the width are hard-split. A non-positive width returns
// the input as a single line.
func wrapText(s string, width int) []string {
	s = strings.TrimSpace(s)
	if width <= 0 || lipgloss.Width(s) <= width {
		return []string{s}
	}

	var lines []string
	var line string
	for _, word := range strings.Fields(s) {
		for lipgloss.Width(word) > width {
			// Hard-split an overlong word.
			if line != "" {
				lines = append(lines, line)
				line = ""
			}
			runes := []rune(word)
			lines = append(lines, string(runes[:width]))
			word = string(runes[width:])
		}
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}
