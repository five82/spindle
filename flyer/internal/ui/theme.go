package ui

import (
	"charm.land/lipgloss/v2"
)

// Theme defines colors for the UI. Content renders on the terminal's default
// background; chrome bands (header, NOW band, footer, tab bar) fill with the
// Surface tone, and the selection bar and status chips carry their own fills.
type Theme struct {
	Name string

	// Background approximates the terminal background; chips use it as
	// their text color against colored fills.
	Background string

	// Surface fills chrome bands, one elevation step above the terminal
	// background (guide two-tone background model).
	Surface string

	// Selection bar
	SelectionBg   string
	SelectionText string

	// Rules and structural lines
	Border string

	// Text colors
	Text    string
	Muted   string
	Faint   string
	Accent  string
	Success string
	Warning string
	Danger  string
	Info    string
}

// Styles returns Lipgloss styles for this theme.
func (t Theme) Styles() Styles {
	return Styles{
		Text: lipgloss.NewStyle().
			Foreground(lipgloss.Color(t.Text)),

		MutedText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(t.Muted)),

		FaintText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(t.Faint)),

		AccentText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(t.Accent)),

		SuccessText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(t.Success)).
			Bold(true),

		WarningText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(t.Warning)),

		DangerText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(t.Danger)).
			Bold(true),

		InfoText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(t.Info)),

		RuleText: lipgloss.NewStyle().
			Foreground(lipgloss.Color(t.Border)),

		Logo: lipgloss.NewStyle().
			Foreground(lipgloss.Color(t.Warning)).
			Bold(true),

		Selected: lipgloss.NewStyle().
			Background(lipgloss.Color(t.SelectionBg)).
			Foreground(lipgloss.Color(t.SelectionText)),

		Band: lipgloss.NewStyle().
			Foreground(lipgloss.Color(t.Text)),
	}
}

// BandStyles returns the theme styles painted onto the Surface background,
// for chrome bands. Band renders separator/padding cells of the fill.
func (t Theme) BandStyles() Styles {
	s := t.Styles()
	bg := lipgloss.Color(t.Surface)
	for _, style := range []*lipgloss.Style{
		&s.Text, &s.MutedText, &s.FaintText, &s.AccentText,
		&s.SuccessText, &s.WarningText, &s.DangerText, &s.InfoText,
		&s.RuleText, &s.Logo, &s.Band,
	} {
		*style = style.Background(bg)
	}
	return s
}

// Styles contains pre-built Lipgloss styles for the theme.
type Styles struct {
	Text        lipgloss.Style
	MutedText   lipgloss.Style
	FaintText   lipgloss.Style
	AccentText  lipgloss.Style
	SuccessText lipgloss.Style
	WarningText lipgloss.Style
	DangerText  lipgloss.Style
	InfoText    lipgloss.Style
	RuleText    lipgloss.Style
	Logo        lipgloss.Style
	Selected    lipgloss.Style
	Band        lipgloss.Style
}

// Theme definitions

var themes = map[string]Theme{
	"Nightfox": nightfoxTheme(),
	"Slate":    slateTheme(),
}

var themeOrder = []string{"Slate", "Nightfox"}

// GetTheme returns a theme by name.
func GetTheme(name string) Theme {
	if t, ok := themes[name]; ok {
		return t
	}
	return slateTheme()
}

// NextTheme returns the next theme name in the cycle.
func NextTheme(current string) string {
	for i, name := range themeOrder {
		if name == current {
			return themeOrder[(i+1)%len(themeOrder)]
		}
	}
	return themeOrder[0]
}

func nightfoxTheme() Theme {
	// Nightfox palette: https://github.com/EdenEast/nightfox.nvim
	return Theme{
		Name: "Nightfox",

		Background: "#131a24", // bg0
		Surface:    "#212e3f", // bg2

		SelectionBg:   "#2b3b51", // sel0
		SelectionText: "#cdcecf", // fg1

		Border: "#39506d", // bg4

		Text:    "#cdcecf", // fg1 (cool gray)
		Muted:   "#738091", // comment (3.3:1 contrast)
		Faint:   "#71839b", // fg3 (3.1:1 contrast)
		Accent:  "#719cd6", // blue
		Success: "#81b29a", // green
		Warning: "#dbc074", // yellow
		Danger:  "#c94f6d", // red
		Info:    "#63cdcf", // cyan
	}
}

func slateTheme() Theme {
	// Tailwind CSS Slate/Sky palette: https://tailwindcss.com/docs/colors
	return Theme{
		Name: "Slate",

		Background: "#020617", // slate-950
		Surface:    "#1e293b", // slate-800

		SelectionBg:   "#0284c7", // sky-600
		SelectionText: "#f8fafc", // slate-50

		Border: "#334155", // slate-700

		Text:    "#f1f5f9", // slate-100
		Muted:   "#94a3b8", // slate-400
		Faint:   "#64748b", // slate-500
		Accent:  "#38bdf8", // sky-400
		Success: "#22c55e", // green-500
		Warning: "#f59e0b", // amber-500
		Danger:  "#ef4444", // red-500
		Info:    "#06b6d4", // cyan-500
	}
}
