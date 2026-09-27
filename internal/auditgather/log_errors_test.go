package auditgather

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/httpapi"
)

func TestGatherLogsKeepsEarlierEventsOnLaterScanFailure(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	logDir := cfg.DaemonLogDir()
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(logDir, "spindle-20260101T000000.000Z.log")
	second := filepath.Join(logDir, "spindle-20260102T000000.000Z.log")
	if err := os.WriteFile(first, []byte("{\"item_id\":1,\"level\":\"INFO\",\"event_type\":\"rip_progress\",\"msg\":\"ripping\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A line longer than the scanner's 1 MiB limit returns a scan error, not a partial line.
	if err := os.WriteFile(second, []byte(strings.Repeat("x", 1<<20+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	item := &httpapi.ItemResponse{ID: 1, CreatedAt: "2026-01-01T00:01:00Z"}
	report, err := gatherLogs(cfg, item)
	if err == nil || !strings.Contains(err.Error(), "scan log") || report == nil || len(report.Events) != 1 || report.Events[0].Message != "ripping" {
		t.Fatalf("logs: %+v %v", report, err)
	}
	if got := getStageDurationSeconds(map[string]any{"stage_duration": "2.5s"}); got != 2.5 {
		t.Fatalf("duration: %v", got)
	}
	if err := scanLogFile(filepath.Join(logDir, "missing"), item, &LogAnalysis{}, time.Time{}); err == nil || !strings.Contains(err.Error(), "open log") {
		t.Fatalf("missing log: %v", err)
	}
}

func TestBuildTaskSummariesPreservesTaskProgress(t *testing.T) {
	tasks := []httpapi.TaskResponse{{Type: "encoding", State: "running", Attempts: 2, Error: "old failure", ActiveAssetKey: "ep1"}}
	tasks[0].Progress.Percent = 42
	tasks[0].Progress.Message = "Encoding ep1"
	got := buildTaskSummaries(tasks)
	if len(got) != 1 || got[0].Type != "encoding" || got[0].Attempts != 2 || got[0].ProgressPercent != 42 || got[0].ProgressMessage != "Encoding ep1" || got[0].ActiveAssetKey != "ep1" {
		t.Fatalf("summary: %+v", got)
	}
}
