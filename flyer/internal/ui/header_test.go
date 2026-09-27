package ui

import (
	"strings"
	"testing"

	"github.com/five82/flyer/internal/spindle"
	"github.com/five82/flyer/internal/state"
)

func TestRenderHeaderShowsOpticalDriveStatus(t *testing.T) {
	tests := []struct {
		name   string
		used   int
		paused bool
		want   string
	}{
		{name: "available", want: "AVAILABLE"},
		{name: "busy", used: 1, want: "BUSY"},
		{name: "paused", paused: true, want: "PAUSED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			theme := GetTheme("Nightfox")
			model := Model{
				width: 80,
				theme: theme,
				snapshot: state.Snapshot{
					HasStatus: true,
					Status: spindle.StatusResponse{
						Running: true,
						Scheduler: &spindle.SchedulerStatus{Resources: map[string]spindle.ResourceStatus{
							"drive": {Capacity: 1, Used: tt.used},
						}},
						Disc: &spindle.DiscStatus{Paused: tt.paused},
					},
				},
			}

			got := model.renderHeader()
			if !strings.Contains(got, "Drive: ") || !strings.Contains(got, tt.want) {
				t.Fatalf("renderHeader() missing drive status %q, got %q", tt.want, got)
			}
		})
	}
}

func TestRenderHeaderOmitsUnknownOpticalDriveStatus(t *testing.T) {
	theme := GetTheme("Nightfox")
	model := Model{
		width: 80,
		theme: theme,
		snapshot: state.Snapshot{
			HasStatus: true,
			Status: spindle.StatusResponse{
				Running:   true,
				Scheduler: &spindle.SchedulerStatus{},
			},
		},
	}

	if got := model.renderHeader(); strings.Contains(got, "Drive:") {
		t.Fatalf("renderHeader() reported unknown drive status, got %q", got)
	}
}

func TestBuildErrorPartsShowsWorkflowLastError(t *testing.T) {
	theme := GetTheme("Nightfox")
	styles := theme.Styles()
	model := Model{
		theme: theme,
		snapshot: state.Snapshot{
			Status: spindle.StatusResponse{
				Workflow: spindle.WorkflowStatus{LastError: "queue persistence failed"},
			},
		},
	}

	parts := model.buildErrorParts(false, styles)
	if len(parts) != 1 {
		t.Fatalf("error parts = %d, want 1", len(parts))
	}
	got := parts[0]
	if !strings.Contains(got, "WORKFLOW") {
		t.Fatalf("workflow label missing from %q", got)
	}
	for _, word := range []string{"queue", "persistence", "failed"} {
		if !strings.Contains(got, word) {
			t.Fatalf("workflow error word %q missing from %q", word, got)
		}
	}
}
