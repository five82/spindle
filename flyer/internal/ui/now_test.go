package ui

import (
	"strings"
	"testing"

	"github.com/five82/spindle/flyer/internal/spindle"
	"github.com/five82/spindle/flyer/internal/state"
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

func TestNowBandDistinguishesReservedEncode(t *testing.T) {
	m := Model{
		width: 80,
		theme: GetTheme("Nightfox"),
		snapshot: state.Snapshot{
			Queue: []spindle.QueueItem{{ID: 42, Tasks: []spindle.Task{
				{Type: "ripping", State: "running"},
				{Type: "encoding", State: "running"},
			}}},
			Status: spindle.StatusResponse{Scheduler: &spindle.SchedulerStatus{Resources: map[string]spindle.ResourceStatus{
				"encode": {Used: 1, Holders: []spindle.ResourceHolder{{ItemID: 42, Task: "encoding"}}},
			}}},
		},
	}
	if got := stripANSI(m.nowBandContent(m.theme.BandStyles())); !strings.Contains(got, "#42 reserved") || strings.Contains(got, "#42 encoding") {
		t.Fatalf("idle encoder in NOW band: %q", got)
	}
	m.snapshot.Queue[0].Tasks[1].ActiveAssetKey = "movie"
	if got := stripANSI(m.nowBandContent(m.theme.BandStyles())); !strings.Contains(got, "#42 encoding") {
		t.Fatalf("active encoder in NOW band: %q", got)
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
