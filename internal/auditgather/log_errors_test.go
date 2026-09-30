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

// An oversized record must be parsed, not end the scan and drop the rest of
// the file; expired creation-time logs must be reported as a coverage gap.
func TestGatherLogsReadsOversizedLinesAndReportsExpiredLogs(t *testing.T) {
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
	huge := "{\"item_id\":1,\"level\":\"WARN\",\"event_type\":\"big\",\"msg\":\"huge\",\"blob\":\"" + strings.Repeat("x", 2<<20) + "\"}\n"
	tail := "{\"item_id\":1,\"level\":\"ERROR\",\"event_type\":\"after\",\"msg\":\"after huge\"}"
	if err := os.WriteFile(second, []byte(huge+tail), 0o644); err != nil {
		t.Fatal(err)
	}
	item := &httpapi.ItemResponse{ID: 1, CreatedAt: "2026-01-01T00:01:00Z"}
	report, errs := gatherLogs(cfg, item)
	if len(errs) != 0 || report == nil || len(report.Events) != 1 || len(report.Warnings) != 1 || len(report.Errors) != 1 || report.Errors[0].Message != "after huge" {
		t.Fatalf("logs: %+v %v", report, errs)
	}

	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	report, errs = gatherLogs(cfg, item)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "expired") || report == nil || len(report.Errors) != 1 {
		t.Fatalf("expired log gap not reported: %v", errs)
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
