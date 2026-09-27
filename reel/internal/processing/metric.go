package processing

import (
	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/quality"
	"github.com/five82/reel/internal/video"
)

// probeMetricFor picks the target-quality probe metric for a source. A
// non-default --target-quality value is CVVDP-denominated, and --cvvdp-display
// overrides the CVVDP model, so either forces CVVDP. The built-in default
// string does not reveal whether the flag was explicitly passed and therefore
// keeps automatic source selection. See quality.ProbeMetricForSource for the
// metric rationale.
func probeMetricFor(cfg *config.Config, inf *video.Info) quality.MetricKind {
	if cfg.ProbeMetric == "cvvdp" || cfg.TargetQuality != config.DefaultTargetQuality || cfg.CVVDPDisplay != "" {
		return quality.MetricCVVDP
	}
	return quality.ProbeMetricForSource(inf)
}
