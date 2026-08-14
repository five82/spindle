package encoder

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/five82/reel"
)

// RunConsole encodes one file in-process with Reel's target-quality mode,
// rendering human-readable progress lines to out. It is the interactive
// sibling of RunWorker: the same encode path, but console rendering instead
// of the JSON wire (no worker subprocess -- a crash takes down only the CLI
// invocation that asked for it).
func RunConsole(ctx context.Context, input, outputDir string, out io.Writer) (*reel.Result, error) {
	enc, err := reel.New(reel.WithQualityMode("target"))
	if err != nil {
		return nil, fmt.Errorf("create reel encoder: %w", err)
	}
	return enc.EncodeWithReporter(ctx, input, outputDir, &consoleReporter{out: out})
}

// consoleReporter prints reporter callbacks as plain lines. Output is
// consumed by operators and agents, not terminals with live meters, so
// progress is throttled to periodic lines rather than redrawn in place.
type consoleReporter struct {
	reel.NullReporter
	out         io.Writer
	lastPercent float32
	lastPrint   time.Time
}

// printf ignores write errors: progress rendering must never abort an encode.
func (r *consoleReporter) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(r.out, format, args...)
}

func (r *consoleReporter) Initialization(s reel.InitializationSummary) {
	r.printf("Input:      %s\n", s.InputFile)
	r.printf("Source:     %s | %s | %s\n", s.Duration, s.Resolution, s.DynamicRange)
	if s.AudioDescription != "" {
		r.printf("Audio:      %s\n", s.AudioDescription)
	}
}

func (r *consoleReporter) CropResult(s reel.CropSummary) {
	if s.Message != "" {
		r.printf("Crop:       %s\n", s.Message)
	}
}

func (r *consoleReporter) EncodingConfig(s reel.EncodingConfigSummary) {
	r.printf("Encoder:    %s preset %s tune %s (%s) | audio %s\n",
		s.Encoder, s.Preset, s.Tune, s.Quality, s.AudioCodec)
}

func (r *consoleReporter) EncodingProgress(p reel.ProgressSnapshot) {
	now := time.Now()
	if p.Percent-r.lastPercent < 5 && now.Sub(r.lastPrint) < 30*time.Second {
		return
	}
	r.lastPercent = p.Percent
	r.lastPrint = now
	line := fmt.Sprintf("Encoding:   %5.1f%%", p.Percent)
	if p.FPS > 0 {
		line += fmt.Sprintf(" | %.0f fps", p.FPS)
	}
	if p.ETA > 0 {
		line += fmt.Sprintf(" | ETA %s", p.ETA.Truncate(time.Second))
	}
	if p.ChunksTotal > 0 {
		line += fmt.Sprintf(" | chunks %d/%d", p.ChunksComplete, p.ChunksTotal)
	}
	r.printf("%s\n", line)
}

func (r *consoleReporter) ValidationComplete(s reel.ValidationSummary) {
	if s.Passed {
		r.printf("Validation: passed\n")
		return
	}
	var failed []string
	for _, step := range s.Steps {
		if !step.Passed {
			failed = append(failed, fmt.Sprintf("%s (%s)", step.Name, step.Details))
		}
	}
	r.printf("Validation: FAILED: %s\n", strings.Join(failed, "; "))
}

func (r *consoleReporter) Warning(message string) {
	r.printf("Warning:    %s\n", message)
}

func (r *consoleReporter) Error(e reel.ReporterError) {
	r.printf("Error:      %s: %s\n", e.Title, e.Message)
	if e.Suggestion != "" {
		r.printf("            suggestion: %s\n", e.Suggestion)
	}
}
