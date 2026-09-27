# Flyer UI Design Language

Status: current (July 2026).

Flyer follows the [Monospace Design TUI standard](https://coreyt.github.io/monospace-design-tui/)
(source: github.com/coreyt/monospace-design-tui). Where flyer's earlier
conventions conflicted with the guide, the guide wins. This document records
how the guide maps onto flyer and which rules are consciously waived.

## Screen model

Two layers, following k9s: a full-width **dashboard** (queue table; glance:
is everything OK, what is running, what needs me) and a full-screen
**inspector** (Enter on an item; drill: one item's pipeline, media, output,
problems, episodes, logs in numbered tabs). Esc returns. The daemon log and
the problems triage list are sibling full-width views of the dashboard
layer.

Invariants worth defending:

- The inspector Overview is a **fixed section skeleton** — Attention,
  Pipeline, Media, Output, Episodes, Meta — in the same order for every
  item state. Rows appear or disappear by data presence, never by state
  branching, so positions stay learnable (tests assert the order).
  Attention leads: the moment an item needs the operator is exactly when
  the problem should hold the top of the screen. The accepted cost is
  that Pipeline shifts down when Attention appears.
- The inspector Problems tab label carries a `⚠` marker when the item
  actually has problems — the glyph marks presence, the label carries the
  meaning — so the operator never tabs into an empty view blind.
- The header always renders reported optical-drive availability (AVAILABLE /
  BUSY / PAUSED). The queue's NOW band reports active resource holders only,
  including the drive holder when busy. "Insert the next disc" is the single
  most useful signal this UI carries, so availability remains visible on every
  view without being duplicated when the drive is idle.
- Header segments, footer hints, and inspector item-band segments carry
  drop-priority ranks; overflowing lines shed whole segments, never crop
  mid-segment.
- Queue selection is item-ID-sticky across refreshes, not row-index-sticky.

## Layout

Every screen is a vertical stack:

```
header band            Surface-filled: logo, daemon/drive health, counts, errors
[NOW band]             Surface-filled, queue view only: live resource holders
[item band + tab band] Surface-filled, inspector only
Level 1 panel          single-line border, title in the top border
[status line]          log views only
footer key strip       Surface-filled, pinned to the bottom row
```

The panel fills the vertical slack so the frame is stable while data changes
(guide: stable spatial anchors). Interior content width is
`panelInnerWidth(width)` = terminal width - 4 (borders plus one space of
padding per side).

Scroll visibility: when panel content overflows, the scroll position renders
right-aligned in the bottom border — a `start-end of total` range for lists,
a percentage for viewports; log views show the percentage in their status
line while follow is paused. The inspector's item line leads with a breadcrumb
naming the view Esc returns to, followed by the prominent item number
(`Queue › ID #1 › Title` or
`Problems › ID #1 › Title`).

Breakpoints: below 80 columns the queue drops its AGE column and header
labels abbreviate; at >= 100 columns the queue's percent column gains an
inline progress bar. 80x24 MUST stay functional — verify layout changes
there.

## Elevation (guide §6)

| Level | Use in flyer | Border |
|-------|--------------|--------|
| 0 | Content inside panels; section rules (`── Title ───`) | none |
| 1 | Every view's content region (queue, problems, logs, inspector tabs) | single-line, title embedded in top border |
| 4 | Help and log-filter modals | double-line, centered over a faint scrim |

Rounded borders are not used on interactive surfaces. New overlays get
double-line borders and composite through `overlayCenter` (which applies the
scrim).

## Backgrounds (two-tone model)

- Content renders on the **terminal's default background**. The theme
  `Background` value only approximates it (chip text color); never paint
  large content areas with it.
- Chrome bands (header, NOW band, footer, inspector item/tab lines) fill
  with the theme **Surface** tone via `Theme.BandStyles()`. Inside a band,
  every rendered run — including space and punctuation glue — must carry the
  Surface background or the band shows holes; use `styles.Band.Render(...)`
  for glue and `padBand` to extend the band to full width.
- Text roles must hold >= 3:1 contrast against **both** the theme background
  and Surface (guide §9.5; body text ideally 4.5:1). When adding or changing
  a theme color, check it against both backdrops — a Faint tone that passes
  on the background can fall under 3:1 on Surface.
- The selection bar and status chips keep their own fills.

## Keyboard

Follows the guide's Tier 1/2 assignments: `q` quits, `?`/`h` help, `/`
filter, `r` refresh, `Esc` back, `g`/`G` top/bottom, `Ctrl+D`/`Ctrl+U`
half-page. Single-letter keys bind both cases and display lowercase.
Documented exceptions: `t` (episodes) vs `T` (theme), and vim's `n`/`N`
match cycling. The footer key strip shows the current context's keys with
drop-priority ranks for narrow terminals; a key not shown in the footer must
not be required to complete a task.

## Waivers (guide rules deliberately not adopted)

- **Shadows on modals (§6.4):** the scrim already dims the whole backdrop, so
  a dim shadow is invisible. Skipped.
- **Three-region sidebar layout (§1.3 Region A):** flyer is a single-column
  read-only monitor; no sidebar navigation exists to put there.
- **Light theme / §5.6 monochrome fallback:** themes are dark-only truecolor;
  lipgloss downsamples color depth automatically. Accepted risk for a
  single-operator tool.
- **Menus, action bar, F-keys, mouse:** interactive-app machinery out of
  scope for a read-only TUI.
- **Entry-field fill characters (§4.4):** bubbles' textinput placeholder
  already communicates the field's presence; underscore fill would mean
  fighting the component for no daily-use gain.
- **User-controllable sort (§11.1 `s`):** the queue's fixed priority order
  (review, failed, live work, then ID) is the operator's priority order;
  re-sorting would mostly create ways to hide problems.
