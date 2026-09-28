package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/five82/spindle/flyer/internal/spindle"
)

// renderNowBand renders the live resource occupancy band under the header:
// which task of which item holds each scheduler resource, with live percent
// and fps/ETA where available. Idle resources are omitted; the header carries
// the optical drive's availability on every view.
func (m Model) renderNowBand() string {
	styles := m.theme.BandStyles()
	return padBand(m.nowBandContent(styles), m.width, styles.Band)
}

// nowBandContent composes the NOW band segments.
func (m Model) nowBandContent(styles Styles) string {
	compact := m.width < compactWidthThreshold

	label := styles.FaintText.Bold(true).Render("NOW ")
	sep := styles.Band.Render(" ") + styles.RuleText.Render("|") + styles.Band.Render(" ")

	sched := m.snapshot.Status.Scheduler
	if sched == nil || len(sched.Resources) == 0 {
		// Old daemon or no scheduler info: fall back to a bare active count.
		if n := m.countProcessingItems(); n > 0 {
			return label + styles.MutedText.Render("Active: ") +
				styles.AccentText.Render(fmt.Sprintf("%d", n))
		}
		return label + styles.FaintText.Render("idle")
	}

	var parts []string
	for _, name := range resourceOrder(m.snapshot.Status.Pipeline, sched.Resources) {
		res := sched.Resources[name]
		rlabel := resourceLabel(name)

		if res.Used == 0 {
			continue
		}

		for _, h := range res.Holders {
			info := stageDisplay(h.Task)
			activity := roleStyle(info.role, styles).Render(strings.ToLower(info.label))
			if h.Task == "encoding" {
				for _, item := range m.snapshot.Queue {
					if item.ID != h.ItemID {
						continue
					}
					for _, task := range item.Tasks {
						if task.Type == h.Task && !task.IsWorking() {
							activity = styles.FaintText.Render("reserved")
						}
					}
					break
				}
			}
			seg := styles.MutedText.Render(rlabel+": ") +
				styles.Text.Render(fmt.Sprintf("#%d ", h.ItemID)) + activity
			if !compact {
				for _, extra := range m.holderExtras(h) {
					seg += styles.AccentText.Render(" " + extra)
				}
			}
			parts = append(parts, seg)
		}
	}

	if len(parts) == 0 {
		return label + styles.FaintText.Render("idle")
	}
	return label + strings.Join(parts, sep)
}

// holderExtras distinguishes a reservation from work, and scopes measured work.
func (m Model) holderExtras(h spindle.ResourceHolder) []string {
	for i := range m.snapshot.Queue {
		item := &m.snapshot.Queue[i]
		if item.ID != h.ItemID {
			continue
		}
		for _, t := range item.Tasks {
			if t.Type != h.Task || t.State != "running" {
				continue
			}
			if m.snapshot.LastError != nil {
				return []string{"stale"}
			}
			now := time.Now()
			if m.now != nil {
				now = m.now()
			}
			for _, activity := range t.Activities {
				if activity.State == "waiting" {
					return []string{activity.Message}
				}
				if activity.State == "running" && !activity.Started().IsZero() && now.Sub(activity.Started()) >= 10*time.Second && activity.Total > 0 && activity.Unit != "" {
					return []string{fmt.Sprintf("%s %s %.0f%%", activity.AssetKey, activity.Operation, 100*float64(activity.Completed)/float64(activity.Total))}
				}
			}
			if !t.IsWorking() {
				return []string{"reserved; activity unavailable"}
			}
			return nil
		}
	}
	return nil
}
