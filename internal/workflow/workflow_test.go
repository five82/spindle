package workflow

import (
	"log/slog"
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func newTestManager(stages []PipelineStage) *Manager {
	m := New(nil, nil, slog.Default())
	m.ConfigureStages(stages)
	return m
}

func TestConfigureStagesBuildsStageMap(t *testing.T) {
	stages := []PipelineStage{
		{Name: "identify", Stage: queue.StageIdentification, Semaphore: SemDisc},
		{Name: "rip", Stage: queue.StageRipping, Semaphore: SemDisc},
		{Name: "encode", Stage: queue.StageEncoding, Semaphore: SemEncode},
		{Name: "organize", Stage: queue.StageOrganizing, Semaphore: SemNone},
	}

	m := newTestManager(stages)
	p := m.pipeline

	if len(p.stageMap) != 4 {
		t.Fatalf("stageMap length = %d, want 4", len(p.stageMap))
	}

	for i, s := range stages {
		got, ok := p.stageMap[s.Stage]
		if !ok {
			t.Errorf("stageMap missing stage %q", s.Stage)
			continue
		}
		if got != i {
			t.Errorf("stageMap[%q] = %d, want %d", s.Stage, got, i)
		}
	}
}

func TestConfigureStagesDerivesStageOrderDiscFirst(t *testing.T) {
	stages := []PipelineStage{
		{Name: "identify", Stage: queue.StageIdentification, Semaphore: SemDisc},
		{Name: "encode", Stage: queue.StageEncoding, Semaphore: SemEncode},
		{Name: "rip", Stage: queue.StageRipping, Semaphore: SemDisc},
		{Name: "organize", Stage: queue.StageOrganizing, Semaphore: SemNone},
	}

	m := newTestManager(stages)
	order := m.pipeline.stageOrder

	if len(order) != 4 {
		t.Fatalf("stageOrder length = %d, want 4", len(order))
	}

	// Disc stages should come first (identification, ripping), then the rest.
	if order[0] != queue.StageIdentification {
		t.Errorf("stageOrder[0] = %q, want %q", order[0], queue.StageIdentification)
	}
	if order[1] != queue.StageRipping {
		t.Errorf("stageOrder[1] = %q, want %q", order[1], queue.StageRipping)
	}
	if order[2] != queue.StageEncoding {
		t.Errorf("stageOrder[2] = %q, want %q", order[2], queue.StageEncoding)
	}
	if order[3] != queue.StageOrganizing {
		t.Errorf("stageOrder[3] = %q, want %q", order[3], queue.StageOrganizing)
	}
}

func TestNextStageReturnsCorrectProgression(t *testing.T) {
	stages := []PipelineStage{
		{Name: "identify", Stage: queue.StageIdentification, Semaphore: SemDisc},
		{Name: "rip", Stage: queue.StageRipping, Semaphore: SemDisc},
		{Name: "encode", Stage: queue.StageEncoding, Semaphore: SemEncode},
		{Name: "organize", Stage: queue.StageOrganizing, Semaphore: SemNone},
	}

	m := newTestManager(stages)

	tests := []struct {
		current queue.Stage
		want    queue.Stage
	}{
		{queue.StageIdentification, queue.StageRipping},
		{queue.StageRipping, queue.StageEncoding},
		{queue.StageEncoding, queue.StageOrganizing},
	}

	for _, tt := range tests {
		got := m.nextStage(tt.current)
		if got != tt.want {
			t.Errorf("nextStage(%q) = %q, want %q", tt.current, got, tt.want)
		}
	}
}

func TestNextStageReturnsCompletedForLastStage(t *testing.T) {
	stages := []PipelineStage{
		{Name: "identify", Stage: queue.StageIdentification, Semaphore: SemDisc},
		{Name: "rip", Stage: queue.StageRipping, Semaphore: SemDisc},
		{Name: "organize", Stage: queue.StageOrganizing, Semaphore: SemNone},
	}

	m := newTestManager(stages)

	got := m.nextStage(queue.StageOrganizing)
	if got != queue.StageCompleted {
		t.Errorf("nextStage(%q) = %q, want %q", queue.StageOrganizing, got, queue.StageCompleted)
	}
}

func TestNextStageReturnsCompletedForUnknownStage(t *testing.T) {
	stages := []PipelineStage{
		{Name: "identify", Stage: queue.StageIdentification, Semaphore: SemDisc},
	}

	m := newTestManager(stages)

	got := m.nextStage(queue.Stage("nonexistent"))
	if got != queue.StageCompleted {
		t.Errorf("nextStage(nonexistent) = %q, want %q", got, queue.StageCompleted)
	}
}
