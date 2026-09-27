package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// HelpModal displays keyboard shortcuts. The section matching the surface
// the user opened help from is listed first.
type HelpModal struct {
	keys    keyMap
	context string
}

// NewHelpModal creates a new help modal. context names the section to list
// first (e.g. "Queue", "Logs").
func NewHelpModal(keys keyMap, context string) *HelpModal {
	return &HelpModal{keys: keys, context: context}
}

// Update handles input for the help modal. Any key closes it.
func (h *HelpModal) Update(msg tea.Msg, keys keyMap) (Modal, tea.Cmd, bool) {
	if _, ok := msg.(tea.KeyPressMsg); ok {
		return h, nil, true // Any key closes help
	}
	return h, nil, false
}

// View renders the help modal box; placement over the dimmed backdrop
// happens in the root View(). When one column would not fit the terminal
// height, the sections flow into two columns instead.
func (h *HelpModal) View(theme Theme, width, height int) string {
	styles := theme.Styles()

	sections := h.keys.HelpSections()
	// List the current surface's section first.
	for i, section := range sections {
		if section.Title == h.context && i > 0 {
			reordered := make([]HelpSection, 0, len(sections))
			reordered = append(reordered, section)
			reordered = append(reordered, sections[:i]...)
			reordered = append(reordered, sections[i+1:]...)
			sections = reordered
			break
		}
	}

	blocks := make([]string, len(sections))
	for i, section := range sections {
		var sb strings.Builder
		sb.WriteString(styles.AccentText.Bold(true).Render(section.Title))
		for _, binding := range section.Bindings {
			help := binding.Help()
			keyStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.Warning)).
				Width(12)
			sb.WriteString("\n")
			sb.WriteString(keyStyle.Render(help.Key))
			sb.WriteString(styles.Text.Render(help.Desc))
		}
		blocks[i] = sb.String()
	}

	title := styles.Text.Bold(true).Render("Keyboard Shortcuts") + "\n" +
		styles.FaintText.Render(strings.Repeat("─", 30))

	// 32-col columns keep the two-column modal within the guide's 72-col
	// dialog maximum.
	const colWidth = 32
	body := strings.Join(blocks, "\n\n")
	modalWidth := 40
	vPad := 1

	// Two-column layout when a single column would overflow the terminal.
	// The compact variant drops the title underline and vertical padding so
	// it fits the 80x24 minimum terminal.
	oneColRows := lipgloss.Height(title) + 1 + lipgloss.Height(body) + 4 // padding + border
	if oneColRows > height && width >= 2*colWidth+12 {
		split, rows := 0, 0
		total := lipgloss.Height(body)
		for i, block := range blocks {
			rows += lipgloss.Height(block) + 1
			split = i + 1
			if rows >= total/2 {
				break
			}
		}
		col := lipgloss.NewStyle().Width(colWidth)
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			col.Render(strings.Join(blocks[:split], "\n\n")),
			"  ",
			col.Render(strings.Join(blocks[split:], "\n\n")))
		// Style.Width includes padding, so add it to keep the joined
		// columns from re-wrapping.
		modalWidth = 2*colWidth + 2 + 4
		title = styles.Text.Bold(true).Render("Keyboard Shortcuts")
		vPad = 0
	}

	// Level 4 modal: double-line border per the guide's elevation model.
	modal := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color(theme.Accent)).
		Padding(vPad, 2).
		Width(modalWidth)

	return modal.Render(title + "\n\n" + body)
}
