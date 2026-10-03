package auditgather

import (
	"context"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
)

func TestAuditStageTimingsWithoutDaemonLogs(t *testing.T) {
	cfg := &config.Config{Paths: config.PathsConfig{StateDir: t.TempDir()}}
	transitions := []queue.Event{
		{Type: "stage_start", Stage: queue.StageEncoding, Time: "2026-01-01T00:00:00Z"},
		{Type: "activity_running", Stage: queue.StageEncoding, EpisodeKey: "main", Substage: "chunking"},
		{Type: "stage_complete", Stage: queue.StageEncoding, Time: "2026-01-01T00:00:12Z", DurationSeconds: 12},
	}
	report, err := Gather(context.Background(), cfg, &httpapi.ItemResponse{ID: 1}, transitions...)
	if err != nil || report.Analysis == nil || len(report.Analysis.StageTimings) != 1 || report.Analysis.StageTimings[0].DurationSeconds != 12 || len(report.Transitions) != 3 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}
