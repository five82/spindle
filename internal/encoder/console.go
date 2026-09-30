package encoder

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/five82/spindle/reel"
)

// RunConsole encodes one file in-process with Reel's target-quality mode,
// rendering human-readable progress lines to out. It is the interactive
// sibling of RunWorker: the same encode path, but console rendering instead
// of the JSON wire (no worker subprocess -- a crash takes down only the CLI
// invocation that asked for it).
func RunConsole(ctx context.Context, input, outputDir string, out io.Writer, quiet bool) (*reel.Result, error) {
	// Reel's decisions (INFO) and warnings print as key=value lines; quiet
	// keeps only warnings.
	level := slog.LevelInfo
	if quiet {
		level = slog.LevelWarn
	}
	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level, ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	}}))
	enc, err := reel.New(reel.WithQualityMode("target"), reel.WithLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("create reel encoder: %w", err)
	}
	return enc.Encode(ctx, input, outputDir, &consoleReporter{out: out, quiet: quiet})
}

// consoleReporter prints reporter callbacks as plain lines. Output is
// consumed by operators and agents, not terminals with live meters, so
// progress is throttled to periodic lines rather than redrawn in place.
type consoleReporter struct {
	reel.NullReporter
	out         io.Writer
	quiet       bool
	lastPercent float32
	lastPrint   time.Time
}

// printf ignores write errors: progress rendering must never abort an encode.
func (r *consoleReporter) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(r.out, format, args...)
}

func (r *consoleReporter) Initialization(s reel.InitializationSummary) {
	if r.quiet {
		return
	}
	r.printf("Input:      %s\n", s.InputFile)
	r.printf("Source:     %s | %s | %s\n", s.Duration, s.Resolution, s.DynamicRange)
	if s.AudioDescription != "" {
		r.printf("Audio:      %s\n", s.AudioDescription)
	}
}

func (r *consoleReporter) CropResult(s reel.CropSummary) {
	if !r.quiet && s.Message != "" {
		r.printf("Crop:       %s\n", s.Message)
	}
}

func (r *consoleReporter) EncodingConfig(s reel.EncodingConfigSummary) {
	if r.quiet {
		return
	}
	r.printf("Encoder:    %s preset %s tune %s (%s) | audio %s\n",
		s.Encoder, s.Preset, s.Tune, s.Quality, s.AudioCodec)
}

func (r *consoleReporter) EncodingProgress(p reel.ProgressSnapshot) {
	if r.quiet {
		return
	}
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
		if !r.quiet {
			r.printf("Validation: passed\n")
		}
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

func (r *consoleReporter) Error(e reel.ReporterError) {
	r.printf("Error:      %s: %s\n", e.Title, e.Message)
	if e.Suggestion != "" {
		r.printf("            suggestion: %s\n", e.Suggestion)
	}
}
