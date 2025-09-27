package main

import (
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/queue"
	"spindle/internal/workflow"
)

type fakeRegistrar struct {
	stages []workflow.Stage
}

func (f *fakeRegistrar) Register(stage workflow.Stage) {
	f.stages = append(f.stages, stage)
}

func TestRegisterStages(t *testing.T) {
	cfg := config.Default()
	cfg.TMDBAPIKey = "test-key"
	cfg.StagingDir = t.TempDir()
	cfg.LibraryDir = t.TempDir()
	cfg.LogDir = t.TempDir()
	cfg.ReviewDir = t.TempDir()

	registrar := &fakeRegistrar{}
	registerStages(registrar, &cfg, nil, zap.NewNop())

	if len(registrar.stages) != 4 {
		t.Fatalf("expected 4 stages registered, got %d", len(registrar.stages))
	}

	expectations := []struct {
		name       string
		trigger    queue.Status
		processing queue.Status
		next       queue.Status
	}{
		{"identifier", queue.StatusPending, queue.StatusIdentifying, queue.StatusIdentified},
		{"ripper", queue.StatusIdentified, queue.StatusRipping, queue.StatusRipped},
		{"encoder", queue.StatusRipped, queue.StatusEncoding, queue.StatusEncoded},
		{"organizer", queue.StatusEncoded, queue.StatusOrganizing, queue.StatusCompleted},
	}

	for i, stage := range registrar.stages {
		if stage == nil {
			t.Fatalf("stage %d is nil", i)
		}
		if stage.Name() != expectations[i].name {
			t.Errorf("stage %d name: expected %q, got %q", i, expectations[i].name, stage.Name())
		}
		if stage.TriggerStatus() != expectations[i].trigger {
			t.Errorf("stage %d trigger: expected %s, got %s", i, expectations[i].trigger, stage.TriggerStatus())
		}
		if stage.ProcessingStatus() != expectations[i].processing {
			t.Errorf("stage %d processing: expected %s, got %s", i, expectations[i].processing, stage.ProcessingStatus())
		}
		if stage.NextStatus() != expectations[i].next {
			t.Errorf("stage %d next: expected %s, got %s", i, expectations[i].next, stage.NextStatus())
		}
	}
}

func TestBuildSocketPath(t *testing.T) {
	cfg := config.Default()
	cfg.LogDir = filepath.Join(t.TempDir(), "logs")

	expected := filepath.Join(cfg.LogDir, "spindle.sock")
	if got := buildSocketPath(&cfg); got != expected {
		t.Fatalf("expected socket path %q, got %q", expected, got)
	}

	if got := buildSocketPath(nil); got != filepath.Join("", "spindle.sock") {
		t.Fatalf("expected default socket path %q, got %q", filepath.Join("", "spindle.sock"), got)
	}
}
