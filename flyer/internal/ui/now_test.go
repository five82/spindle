package ui

import (
	"strings"
	"testing"
	"time"

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
	// The Encode resource already names the work; only a reservation is called out.
	if got := stripANSI(m.nowBandContent(m.theme.BandStyles())); got != "NOW Encode: #42" {
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

func TestNowBandNamesHolderTitleAndETA(t *testing.T) {
	now := time.Now()
	m := Model{
		width: 140,
		theme: GetTheme("Slate"),
		now:   func() time.Time { return now },
		snapshot: state.Snapshot{
			Queue: []spindle.QueueItem{{ID: 1, DisplayTitle: "Breaking Bad (2008)", Tasks: []spindle.Task{{Type: "encoding", State: "running", ActiveAssetKey: "s01_001",
				Activities: []spindle.Activity{{ID: "video", Operation: "encoding", AssetKey: "s01_001", State: "running", StartedAt: now.Add(-time.Minute).Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339), Completed: 8, Total: 100, Unit: "frames"}},
				Encoding:   &spindle.EncodingStatus{ETASeconds: 1320}}}}},
			Status: spindle.StatusResponse{Scheduler: &spindle.SchedulerStatus{Resources: map[string]spindle.ResourceStatus{
				"encode": {Used: 1, Holders: []spindle.ResourceHolder{{ItemID: 1, Task: "encoding"}}},
			}}},
		},
	}
	got := stripANSI(m.nowBandContent(m.theme.BandStyles()))
	if got != "NOW Encode: #1 Breaking Bad (2008) · s01_001 8% · ~22m left" {
		t.Fatalf("NOW band = %q", got)
	}

	// Narrow terminals shed the title, then the ETA, before the measure.
	for _, tc := range []struct {
		width int
		want  string
	}{
		{50, "NOW Encode: #1 · s01_001 8% · ~22m left"},
		{35, "NOW Encode: #1 · s01_001 8%"},
		{20, "NOW Encode: #1"},
	} {
		m.width = tc.width
		if got := stripANSI(m.nowBandContent(m.theme.BandStyles())); got != tc.want {
			t.Errorf("width %d: NOW band = %q, want %q", tc.width, got, tc.want)
		}
	}
}
