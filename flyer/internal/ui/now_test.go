package ui

import (
	"strings"
	"testing"

	"github.com/five82/flyer/internal/spindle"
	"github.com/five82/flyer/internal/state"
)

func TestNowBandOmitsIdleDriveState(t *testing.T) {
	for _, tt := range []struct {
		name   string
		paused bool
	}{
		{name: "available"},
		{name: "paused", paused: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{
				width: 80,
				theme: GetTheme("Nightfox"),
				snapshot: state.Snapshot{Status: spindle.StatusResponse{
					Scheduler: &spindle.SchedulerStatus{Resources: map[string]spindle.ResourceStatus{
						"drive": {Capacity: 1},
					}},
					Disc: &spindle.DiscStatus{Paused: tt.paused},
				}},
			}

			got := stripANSI(m.nowBandContent(m.theme.BandStyles()))
			if got != "NOW idle" {
				t.Fatalf("nowBandContent() = %q, want %q", got, "NOW idle")
			}
		})
	}
}

func TestNowBandShowsBusyDriveHolder(t *testing.T) {
	m := Model{
		width: 80,
		theme: GetTheme("Nightfox"),
		snapshot: state.Snapshot{Status: spindle.StatusResponse{
			Scheduler: &spindle.SchedulerStatus{Resources: map[string]spindle.ResourceStatus{
				"drive": {
					Capacity: 1,
					Used:     1,
					Holders:  []spindle.ResourceHolder{{ItemID: 42, Task: "ripping"}},
				},
			}},
		}},
	}

	got := stripANSI(m.nowBandContent(m.theme.BandStyles()))
	if !strings.Contains(got, "Drive: #42 ripping") {
		t.Fatalf("nowBandContent() missing busy drive holder: %q", got)
	}
}
