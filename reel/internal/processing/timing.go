package processing

import (
	"log/slog"
	"time"

	"github.com/five82/spindle/reel/internal/perf"
	"github.com/five82/spindle/reel/internal/reporter"
)

// startStep times a step and logs its wall window at DEBUG when the returned
// stop func runs.
func startStep(log *slog.Logger, name string) func() {
	start := time.Now()
	return func() {
		stop := time.Now()
		log.Debug("phase finished", "phase", name, "started_at", start.Format(time.RFC3339),
			"duration_seconds", stop.Sub(start).Round(time.Millisecond).Seconds())
	}
}

// startPhase times a pipeline phase like startStep and, when the returned
// stop func runs, also records the phase wall window into the perf collector
// for perf.json. The collector is nil-safe.
func startPhase(c *perf.Collector, rep reporter.Reporter, log *slog.Logger, name string) func() {
	start := time.Now()
	update := reporter.StageProgress{Lane: "work", State: "running", Stage: name, Message: name}
	if name == "Video encoding" {
		update.Lane, update.Stage = "video", "encoding"
	}
	rep.StageProgress(update)
	finish := startStep(log, name)
	return func() {
		finish()
		c.RecordPhase(name, start, time.Now())
		update.State, update.Message = "ended", name+" ended"
		rep.StageProgress(update)
	}
}

// phaseTracker sequences named perf/reporter phases through one place so a
// single deferred end() closes whatever phase is open on early return, instead
// of every error path calling the finish func by hand.
type phaseTracker struct {
	perfc  *perf.Collector
	rep    reporter.Reporter
	log    *slog.Logger
	finish func()
}

func newPhaseTracker(perfc *perf.Collector, rep reporter.Reporter, log *slog.Logger) *phaseTracker {
	return &phaseTracker{perfc: perfc, rep: rep, log: log}
}

// start opens a phase, first closing the previous one if it is still open.
func (p *phaseTracker) start(name string) {
	p.end()
	p.finish = startPhase(p.perfc, p.rep, p.log, name)
}

// end closes the open phase; a no-op when none is open, so `defer p.end()`
// covers every early return.
func (p *phaseTracker) end() {
	if p.finish != nil {
		p.finish()
		p.finish = nil
	}
}
