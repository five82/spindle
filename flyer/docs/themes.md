# Theme Guidelines

Flyer supports multiple color themes defined in `internal/ui/theme.go`. When modifying themes:

1. **Use official palettes only** - do not invent colors
2. **Follow established UI hierarchies** - reference canonical implementations
3. **Maintain proper contrast** - text colors must hold >= 3:1 against both the terminal's default background and the theme's Surface tone (chrome bands); body text ideally 4.5:1

Content renders on the terminal's default background; chrome bands (header,
NOW band, footer, inspector tab bar) fill with the Surface tone (see
[design.md](design.md)). Each theme supplies:

| Theme role | Usage |
|------------|-------|
| Background | Approximates the terminal background; chip text color against colored fills |
| Surface | Chrome band fill, one elevation step above the terminal background |
| SelectionBg / SelectionText | The selected-row bar |
| Border | Rules, panel borders, structural lines |
| Text / Muted / Faint | Text hierarchy |
| Accent / Success / Warning / Danger / Info | Semantic color roles |

## Nightfox

**Spec**: https://github.com/EdenEast/nightfox.nvim

| Role | Hex | Usage |
|------|-----|-------|
| bg0 | `#131a24` | Background (chip text) |
| bg2 | `#212e3f` | Surface (chrome bands) |
| sel0 | `#2b3b51` | Selection bar |
| bg4 | `#39506d` | Rules/borders |
| fg1 | `#cdcecf` | Primary text (cool gray) |
| comment | `#738091` | Muted text (3.3:1 contrast) |
| fg3 | `#71839b` | Dimmest text (3.1:1 contrast) |
| blue | `#719cd6` | Accent |
| cyan | `#63cdcf` | Info |
| green | `#81b29a` | Success |
| yellow | `#dbc074` | Warning |
| red | `#c94f6d` | Error/danger |

## Slate

**Palette**: https://tailwindcss.com/docs/colors

| Role | Tailwind | Hex | Usage |
|------|----------|-----|-------|
| Background | slate-950 | `#020617` | Background (chip text) |
| Surface | slate-800 | `#1e293b` | Surface (chrome bands) |
| SelectionBg | sky-600 | `#0284c7` | Selection bar |
| Border | slate-700 | `#334155` | Rules/borders |
| Text | slate-100 | `#f1f5f9` | Primary text |
| Muted | slate-400 | `#94a3b8` | Muted text |
| Faint | slate-500 | `#64748b` | Dimmest text |
| Accent | sky-400 | `#38bdf8` | Accent |

Use semantic color roles (Success, Warning, Danger, Info) mapped to appropriate palette colors for each theme.
