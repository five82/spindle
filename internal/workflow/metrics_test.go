package workflow

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

func TestWriteMetricsRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.jsonl")

	m := New(nil, nil, nil, slog.Default())
	m.SetMetricsPath(path)

	env := ripspec.Envelope{
		Version: ripspec.CurrentVersion,
		Metadata: ripspec.Metadata{
			Title:      "Example Movie",
			MediaType:  "movie",
			DiscSource: "bluray",
		},
		Attributes: ripspec.EnvelopeAttributes{
			Rip: &ripspec.RipStats{
				Device:      "/dev/sr0",
				DriveVendor: "PIONEER",
				DriveModel:  "BD-RW BDR-2213",
				Bytes:       30 << 30,
				Seconds:     1800,
				Titles:      1,
			},
			EncodeStats: []ripspec.EncodeStats{{
				EpisodeKey:      "main",
				Width:           1920,
				Height:          1080,
				ResolutionClass: "1080p",
				EncodeSeconds:   900,
				Speed:           4.5,
			}},
		},
	}
	raw, err := env.Encode()
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}

	item := &queue.Item{
		ID:          7,
		DiscTitle:   "Example Movie",
		CreatedAt:   time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		RipSpecData: raw,
	}
	tasks := []*queue.Task{{
		ItemID:     7,
		Type:       queue.StageRipping,
		StartedAt:  time.Now().Add(-50 * time.Minute).UTC().Format(time.RFC3339Nano),
		FinishedAt: time.Now().Add(-20 * time.Minute).UTC().Format(time.RFC3339Nano),
	}}

	// Simulate an accumulated resource wait for the ripping stage.
	m.waits[7] = map[queue.Stage]float64{queue.StageRipping: 12.5}

	m.writeMetricsRecord(item, tasks)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read metrics file: %v", err)
	}
	var rec metricsRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if rec.Schema != 1 || rec.ItemID != 7 || rec.Title != "Example Movie" {
		t.Errorf("identity fields wrong: %+v", rec)
	}
	if rec.MediaType != "movie" || rec.DiscType != "bluray" {
		t.Errorf("envelope metadata not carried: %+v", rec)
	}
	if rec.Rip == nil || rec.Rip.DriveModel != "BD-RW BDR-2213" {
		t.Errorf("rip stats not carried: %+v", rec.Rip)
	}
	if len(rec.Encodes) != 1 || rec.Encodes[0].ResolutionClass != "1080p" {
		t.Errorf("encode stats not carried: %+v", rec.Encodes)
	}
	if len(rec.Stages) != 1 || rec.Stages[0].Stage != "ripping" {
		t.Fatalf("stages wrong: %+v", rec.Stages)
	}
	if rec.Stages[0].Seconds < 1799 || rec.Stages[0].Seconds > 1801 {
		t.Errorf("ripping seconds = %.1f, want ~1800", rec.Stages[0].Seconds)
	}
	if rec.Stages[0].WaitSeconds != 12.5 {
		t.Errorf("wait seconds = %.1f, want 12.5", rec.Stages[0].WaitSeconds)
	}
	if rec.TotalWallSeconds < 3599 {
		t.Errorf("total wall seconds = %.1f, want ~3600", rec.TotalWallSeconds)
	}
	if _, ok := m.waits[7]; ok {
		t.Error("waits entry should be consumed by the record")
	}

	// A second write appends a second line.
	m.writeMetricsRecord(item, tasks)
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read metrics file: %v", err)
	}
	if lines := len(splitNonEmptyLines(data)); lines != 2 {
		t.Errorf("lines = %d, want 2", lines)
	}
}

func splitNonEmptyLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

func TestWriteMetricsRecordDisabledByDefault(t *testing.T) {
	m := New(nil, nil, nil, slog.Default())
	// No path set: must be a no-op, not a panic or a write to "".
	m.writeMetricsRecord(&queue.Item{ID: 1}, nil)
}
