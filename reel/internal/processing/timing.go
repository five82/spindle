package processing

import (
	"fmt"
	"time"

	"github.com/five82/reel/internal/perf"
	"github.com/five82/reel/internal/reporter"
)

func startVerboseStep(rep reporter.Reporter, name string) func() {
	start := time.Now()
	rep.Verbose(fmt.Sprintf("%s started at %s", name, start.Format(time.RFC3339)))
	return func() {
		stop := time.Now()
		rep.Verbose(fmt.Sprintf("%s stopped at %s (duration %s)", name, stop.Format(time.RFC3339), stop.Sub(start).Round(time.Millisecond)))
	}
}

// startPhase times a pipeline phase: it emits the same verbose start/stop lines
// as startVerboseStep and, when the returned stop func runs, records the phase
// wall window into the perf collector for perf.json. The collector is nil-safe,
// so callers without one still get verbose logging.
func startPhase(c *perf.Collector, rep reporter.Reporter, name string) func() {
	start := time.Now()
	rep.Verbose(fmt.Sprintf("%s started at %s", name, start.Format(time.RFC3339)))
	return func() {
		stop := time.Now()
		rep.Verbose(fmt.Sprintf("%s stopped at %s (duration %s)", name, stop.Format(time.RFC3339), stop.Sub(start).Round(time.Millisecond)))
		c.RecordPhase(name, start, stop)
	}
}

// phaseTracker sequences named perf/reporter phases through one place so a
// single deferred end() closes whatever phase is open on early return, instead
// of every error path calling the finish func by hand.
type phaseTracker struct {
	perfc  *perf.Collector
	rep    reporter.Reporter
	finish func()
}

func newPhaseTracker(perfc *perf.Collector, rep reporter.Reporter) *phaseTracker {
	return &phaseTracker{perfc: perfc, rep: rep}
}

// start opens a phase, first closing the previous one if it is still open.
func (p *phaseTracker) start(name string) {
	p.end()
	p.finish = startPhase(p.perfc, p.rep, name)
}

// end closes the open phase; a no-op when none is open, so `defer p.end()`
// covers every early return.
func (p *phaseTracker) end() {
	if p.finish != nil {
		p.finish()
		p.finish = nil
	}
}
