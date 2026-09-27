// Package perf collects per-file pipeline timing and adaptive-worker history
// and writes a perf.json artifact into the work directory.
//
// The artifact gives performance attribution beyond verbose logs and throwaway
// scripts: it records the wall time of each pipeline phase (analysis, crop,
// shot detection, chunk planning, resume, audio, video encode/target-quality,
// merge, mux, validation, stream-size scans) and a sampled history of the
// adaptive encode scheduler (active/target/max workers, in-flight chunks,
// metric workers, and cumulative encode-slot wait time).
//
// perf.json lives in the work directory, so it is written only when that
// directory will be kept: after a successful encode with --keep-workdir, and
// after a failed encode whose work directory survives for resume (the final
// output was not produced). That matches the performance-testing workflow,
// which keeps the work directory to read target-quality.json and the per-chunk
// logs, and still yields partial timing when an encode fails partway.
//
// Phases may overlap: audio extraction runs concurrently with video encoding
// and merging, so the sum of phase durations can exceed total_seconds. Each
// phase records its real wall-clock window (start_seconds, duration_seconds)
// relative to the run start so analysis tooling can reconstruct the timeline.
package perf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// schemaVersion is bumped when the perf.json layout changes incompatibly.
const schemaVersion = 1

// workerSampleMinInterval throttles unchanged worker-history samples. A sample
// is always recorded when any worker count changes; otherwise samples are kept
// no closer together than this so a feature-length encode stays bounded.
const workerSampleMinInterval = 2.0

// Meta holds the static description of an encoded file. Fields are flattened
// into the top level of perf.json.
type Meta struct {
	SchemaVersion      int     `json:"schema_version"`
	InputFile          string  `json:"input_file"`
	OutputFile         string  `json:"output_file"`
	Width              uint32  `json:"width"`
	Height             uint32  `json:"height"`
	HDR                bool    `json:"hdr"`
	DurationSeconds    float64 `json:"duration_seconds"`
	Frames             int     `json:"frames"`
	QualityMode        string  `json:"quality_mode"`
	TargetQuality      string  `json:"target_quality,omitempty"`
	CRF                float32 `json:"crf,omitempty"`
	Preset             uint8   `json:"preset"`
	Denoise            string  `json:"denoise,omitempty"`
	MetricWorkers      int     `json:"metric_workers"`
	MaxAdaptiveWorkers int     `json:"max_adaptive_workers"`
	Chunks             int     `json:"chunks"`
	SVTAV1Version      string  `json:"svtav1_version,omitempty"`
	Hostname           string  `json:"hostname,omitempty"`
}

// Phase is the wall-clock window of one pipeline phase, anchored to run start.
type Phase struct {
	Name            string  `json:"name"`
	StartSeconds    float64 `json:"start_seconds"`
	DurationSeconds float64 `json:"duration_seconds"`
}

// TargetQualityMetricStats aggregates the CRF search outcomes for the chunks
// scored with one metric. SSIMU2 runs mix scales (warmup chunks carry JOD
// scores), so a run can produce one entry per metric.
type TargetQualityMetricStats struct {
	Metric            string         `json:"metric"`
	Target            float64        `json:"target"`
	Tolerance         float64        `json:"tolerance"`
	Chunks            int            `json:"chunks"`
	Probes            int            `json:"probes"`
	ProbesPerChunk    float64        `json:"probes_per_chunk"`
	ScoreMin          float64        `json:"score_min"`
	ScoreMean         float64        `json:"score_mean"`
	ScoreMax          float64        `json:"score_max"`
	ScoreMeanAbsError float64        `json:"score_mean_abs_error"`
	FinalCRFMin       float64        `json:"final_crf_min"`
	FinalCRFMedian    float64        `json:"final_crf_median"`
	FinalCRFMax       float64        `json:"final_crf_max"`
	StopReasons       map[string]int `json:"stop_reasons"`
	InitialCRFSources map[string]int `json:"initial_crf_sources"`
}

// TargetQualityStats summarizes a target-quality run. The calibration offset
// is the per-title CVVDP->SSIMU2 shift measured during warmup; grainy or
// complex titles calibrate low, so it doubles as a content-complexity signal.
type TargetQualityStats struct {
	SSIMU2CalibrationOffset *float64                   `json:"ssimu2_calibration_offset,omitempty"`
	Metrics                 []TargetQualityMetricStats `json:"metrics"`
}

// GrainTreatmentStats records the grain-treatment gate's verdict for one
// title: what was measured, which thresholds it was compared against, what
// treatment (if any) the encode ran with, and how much quality the denoiser
// itself costs. The gate has two stages: a fixed-CRF bits-per-pixel median,
// and (only when that median is ambiguous) a measurement of what the same
// samples cost at the quality target. Target-quality scores are measured against the denoised
// reference, so DenoiseCeilingJODMean/Min are the honest ceiling those scores
// sit under.
type GrainTreatmentStats struct {
	// Mode is how the treatment was decided: "auto" (the gate ran), "off"
	// (disabled by the user), or "override" (explicit experimental flags).
	Mode    string `json:"mode"`
	Treated bool   `json:"treated"`
	// Tier is "estimated" for source-matched grain. Historical reports used
	// "light"/"med"; retained so embedders can still read those reports.
	Tier            string `json:"tier,omitempty"`
	ResolutionClass string `json:"resolution_class"`
	// Denoise and GrainTable are what the encode actually ran with.
	Denoise    string                `json:"denoise,omitempty"`
	GrainTable string                `json:"grain_table,omitempty"`
	Estimation *GrainEstimationStats `json:"estimation,omitempty"`
	// Reason explains a verdict the numbers alone do not (no eligible sample
	// chunks, SD source, explicit override).
	Reason string `json:"reason,omitempty"`

	GateCRF        float64   `json:"gate_crf,omitempty"`
	SampleChunks   []int     `json:"sample_chunks,omitempty"`
	SampleBPP      []float64 `json:"sample_bpp,omitempty"`
	MedianBPP      float64   `json:"median_bpp,omitempty"`
	LightBPPCutoff float64   `json:"light_bpp_cutoff,omitempty"`
	// MedBPPCutoff is retained for historical reports; estimation has no tiers.
	MedBPPCutoff   float64 `json:"med_bpp_cutoff,omitempty"`
	GateSeconds    float64 `json:"gate_seconds,omitempty"`
	CeilingSeconds float64 `json:"ceiling_seconds,omitempty"`

	// GateStage is which stage decided the verdict: "bpp" (the fixed-CRF
	// median alone) or "tq_probe" (the median landed in the ambiguous band
	// and the samples were re-measured at the quality target). The ambiguous
	// band is AmbiguousBPPCutoff (inclusive) to LightBPPCutoff (exclusive).
	GateStage          string  `json:"gate_stage,omitempty"`
	AmbiguousBPPCutoff float64 `json:"ambiguous_bpp_cutoff,omitempty"`
	// Stage2DeliveredBPP is what each sample chunk costs at the quality
	// target, compared against the same treatment cutoff as the fixed-CRF
	// median. Stage2Probes counts the probe encodes it took across all
	// samples; Stage2Error records why a refinement that was due did not
	// happen (the fixed-CRF verdict then stands).
	Stage2DeliveredBPP []float64 `json:"stage2_delivered_bpp,omitempty"`
	Stage2MedianBPP    float64   `json:"stage2_median_bpp,omitempty"`
	Stage2Probes       int       `json:"stage2_probes,omitempty"`
	Stage2Seconds      float64   `json:"stage2_seconds,omitempty"`
	Stage2Error        string    `json:"stage2_error,omitempty"`

	DenoiseCeilingJODMean *float64 `json:"denoise_ceiling_jod_mean,omitempty"`
	DenoiseCeilingJODMin  *float64 `json:"denoise_ceiling_jod_min,omitempty"`
	// CeilingMeasured records a completed paired-frame pass. CeilingError
	// remains for historical best-effort reports; automatic treatment now
	// fails if this pass cannot supply a valid grain model.
	CeilingMeasured bool   `json:"ceiling_measured,omitempty"`
	CeilingError    string `json:"ceiling_error,omitempty"`
	// BandTopJOD is the top of the configured target-quality band, recorded
	// so consumers can judge the ceiling against the band without
	// duplicating the constant.
	BandTopJOD float64 `json:"band_top_jod,omitempty"`
	// Reused marks a verdict replayed from the work directory on resume:
	// GateSeconds/CeilingSeconds then describe the run that measured them,
	// not this one.
	Reused bool `json:"reused,omitempty"`
}

// GrainEstimationStats records the bounded analysis that generated the table.
// The exact table lives in grain-gate.json; SHA256 also pins resume identity.
// Seconds is sampler/fitter wall time within CeilingSeconds, not an additional
// full decode pass, and describes the original run when the verdict is Reused.
type GrainEstimationStats struct {
	Version        string  `json:"version"`
	SHA256         string  `json:"sha256"`
	Frames         []int   `json:"frames"`
	AcceptedFrames int     `json:"accepted_frames"`
	Patches        int     `json:"patches"`
	Seconds        float64 `json:"seconds"`
}

// WorkerSummary condenses the sampled worker history: a time-weighted mean of
// active encode workers, the peak, and the cumulative time chunks spent
// waiting for an encode slot. MeanActive near Meta.MaxAdaptiveWorkers means
// the host was saturated.
type WorkerSummary struct {
	MeanActive            float64 `json:"mean_active"`
	PeakActive            int     `json:"peak_active"`
	EncodeSlotWaitSeconds float64 `json:"encode_slot_wait_seconds"`
}

// Report is the structured performance summary of one encode, returned to
// library callers and embedded (with the raw worker history) in perf.json.
type Report struct {
	Meta
	TotalSeconds float64       `json:"total_seconds"`
	Phases       []Phase       `json:"phases"`
	Workers      WorkerSummary `json:"workers"`
	// TargetQualityStats is named to avoid colliding with Meta.TargetQuality
	// (the configured target range string) in the flattened JSON.
	TargetQualityStats *TargetQualityStats  `json:"target_quality_stats,omitempty"`
	GrainTreatment     *GrainTreatmentStats `json:"grain_treatment,omitempty"`
}

// WorkerSample is a point-in-time snapshot of the adaptive encode scheduler.
type WorkerSample struct {
	TSeconds              float64 `json:"t_seconds"`
	Active                int     `json:"active"`
	Target                int     `json:"target"`
	Max                   int     `json:"max"`
	InFlight              int     `json:"in_flight"`
	ChunksComplete        int     `json:"chunks_complete"`
	FramesComplete        int     `json:"frames_complete"`
	EncodeSlotWaitSeconds float64 `json:"encode_slot_wait_seconds"`
}

// Collector accumulates phase timings and worker history for one encoded file.
//
// It is safe for concurrent use: phases are recorded from the orchestration
// goroutine while worker samples arrive from the encode progress callback. All
// methods are nil-safe so callers can pass a nil *Collector to disable
// collection without branching at every call site.
type Collector struct {
	mu      sync.Mutex
	start   time.Time
	workDir string
	meta    Meta
	phases  []Phase
	samples []WorkerSample
	tq      *TargetQualityStats
	grain   *GrainTreatmentStats

	haveSample bool
	lastSample WorkerSample
}

// New returns a Collector whose timeline is anchored to now.
func New() *Collector {
	return &Collector{
		start: time.Now(),
		meta:  Meta{SchemaVersion: schemaVersion},
	}
}

// SetWorkDir records where Write should emit perf.json. Until it is set, Write
// is a no-op (there is no artifact directory to anchor to).
func (c *Collector) SetWorkDir(dir string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.workDir = dir
	c.mu.Unlock()
}

// UpdateMeta mutates the file metadata under the collector lock.
func (c *Collector) UpdateMeta(fn func(m *Meta)) {
	if c == nil || fn == nil {
		return
	}
	c.mu.Lock()
	fn(&c.meta)
	c.mu.Unlock()
}

// RecordPhase appends a finished phase. start and stop are absolute timestamps;
// the phase is stored relative to the collector's run start.
func (c *Collector) RecordPhase(name string, start, stop time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.phases = append(c.phases, Phase{
		Name:            name,
		StartSeconds:    start.Sub(c.start).Seconds(),
		DurationSeconds: stop.Sub(start).Seconds(),
	})
	c.mu.Unlock()
}

// RecordWorkerSample appends an adaptive-scheduler snapshot. The caller leaves
// TSeconds zero; the collector stamps it relative to run start. Samples whose
// worker counts are unchanged from the previous sample and that arrive within
// workerSampleMinInterval are dropped to bound the history size.
func (c *Collector) RecordWorkerSample(s WorkerSample) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s.TSeconds = time.Since(c.start).Seconds()
	if c.haveSample {
		last := c.lastSample
		unchanged := s.Active == last.Active && s.Target == last.Target &&
			s.Max == last.Max && s.InFlight == last.InFlight
		if unchanged && s.TSeconds-last.TSeconds < workerSampleMinInterval {
			return
		}
	}
	c.samples = append(c.samples, s)
	c.lastSample = s
	c.haveSample = true
}

// SetTargetQuality attaches the target-quality search summary to the report.
func (c *Collector) SetTargetQuality(s *TargetQualityStats) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.tq = s
	c.mu.Unlock()
}

// SetGrainTreatment attaches the grain-treatment gate verdict to the report.
func (c *Collector) SetGrainTreatment(s *GrainTreatmentStats) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.grain = s
	c.mu.Unlock()
}

// Report returns the structured summary of the run so far. It is safe to call
// on a nil collector (returns nil) and does not require a work directory.
func (c *Collector) Report() *Report {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.report()
	return &r
}

// report builds the Report under the collector lock.
func (c *Collector) report() Report {
	phases := append([]Phase(nil), c.phases...)
	sort.SliceStable(phases, func(i, j int) bool {
		return phases[i].StartSeconds < phases[j].StartSeconds
	})
	return Report{
		Meta:               c.meta,
		TotalSeconds:       time.Since(c.start).Seconds(),
		Phases:             phases,
		Workers:            summarizeWorkers(c.samples),
		TargetQualityStats: c.tq,
		GrainTreatment:     c.grain,
	}
}

// summarizeWorkers reduces the sample history to a time-weighted mean and peak
// of active workers plus the final cumulative encode-slot wait. Each sample's
// Active is weighted by the interval until the next sample; the last sample
// gets no weight (its interval is unknown), which matters little because
// samples are dense during the encode.
func summarizeWorkers(samples []WorkerSample) WorkerSummary {
	var s WorkerSummary
	if len(samples) == 0 {
		return s
	}
	var weighted, span float64
	for i, sample := range samples {
		if sample.Active > s.PeakActive {
			s.PeakActive = sample.Active
		}
		if i+1 < len(samples) {
			dt := samples[i+1].TSeconds - sample.TSeconds
			weighted += float64(sample.Active) * dt
			span += dt
		}
	}
	if span > 0 {
		s.MeanActive = weighted / span
	} else {
		s.MeanActive = float64(samples[len(samples)-1].Active)
	}
	s.EncodeSlotWaitSeconds = samples[len(samples)-1].EncodeSlotWaitSeconds
	return s
}

// Write emits perf.json into the work directory. It is best-effort: if no work
// directory was set (for example the encode never reached the chunked pipeline)
// it returns nil without writing.
func (c *Collector) Write() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.workDir == "" {
		return nil
	}

	out := struct {
		Report
		WorkerHistory []WorkerSample `json:"worker_history"`
	}{
		Report:        c.report(),
		WorkerHistory: c.samples,
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(c.workDir, "perf.json"), data, 0644)
}
