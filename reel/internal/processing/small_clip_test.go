package processing

import (
	"context"
	"testing"

	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/reporter"
)

func TestProcessVideosTinySyntheticClip(t *testing.T) {
	dir := t.TempDir()
	input := shortY4M(t, dir)
	cfg := config.NewConfig(input, dir, dir)
	cfg.TempDir = dir
	cfg.OutputDir = dir
	cfg.QualityMode = config.QualityModeCRF
	cfg.CropMode = "none"
	rep := &clipReporter{}
	results, err := ProcessVideos(context.Background(), cfg, []string{input}, "output.mkv", rep)
	if err != nil || len(results) != 1 {
		t.Fatalf("results = %+v, error = %v, reports = %+v", results, err, rep.errors)
	}
	if !results[0].ValidationPassed {
		t.Fatalf("validation: %+v", results[0].ValidationSteps)
	}
}

type clipReporter struct {
	reporter.NullReporter
	errors []reporter.ReporterError
}

func (r *clipReporter) Error(e reporter.ReporterError) { r.errors = append(r.errors, e) }
