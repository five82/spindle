package encode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/five82/reel/internal/perf"
	"github.com/five82/reel/internal/quality"
)

// chunkTargetLog records one chunk's full CRF search for the per-chunk and
// aggregate target-quality JSON artifacts (and for prior seeding on resume).
type chunkTargetLog struct {
	ChunkIdx         int                `json:"chunk_idx"`
	Frames           int                `json:"frames"`
	Metric           string             `json:"metric,omitempty"`
	Target           float32            `json:"target"`
	Tolerance        float32            `json:"tolerance"`
	CRFMin           float32            `json:"crf_min"`
	CRFMax           float32            `json:"crf_max"`
	InitialCRF       float32            `json:"initial_crf"`
	InitialCRFSource string             `json:"initial_crf_source"`
	Probes           []quality.Probe    `json:"probes"`
	FinalCRF         float32            `json:"final_crf"`
	FinalScore       float32            `json:"final_score"`
	FinalSize        uint64             `json:"final_size"`
	StopReason       quality.StopReason `json:"stop_reason"`
	StartedAt        time.Time          `json:"started_at"`
	CompletedAt      time.Time          `json:"completed_at"`
}

// targetQualityStats builds the structured aggregate returned to library
// callers. It mirrors logTargetAggregate's per-metric grouping: SSIMU2 runs
// mix scales (warmup chunks carry JOD scores), so stats are grouped by the
// metric that scored each chunk.
func targetQualityStats(logs []chunkTargetLog, calibration *ssimu2Calibration) *perf.TargetQualityStats {
	if len(logs) == 0 {
		return nil
	}
	byMetric := make(map[string][]chunkTargetLog)
	for _, log := range logs {
		name := log.Metric
		if name == "" {
			name = string(quality.MetricCVVDP)
		}
		byMetric[name] = append(byMetric[name], log)
	}
	stats := &perf.TargetQualityStats{}
	if calibration != nil {
		if offset, locked := calibration.Offset(); locked {
			v := float64(offset)
			stats.SSIMU2CalibrationOffset = &v
		}
	}
	metricNames := make([]string, 0, len(byMetric))
	for name := range byMetric {
		metricNames = append(metricNames, name)
	}
	sort.Strings(metricNames)
	for _, name := range metricNames {
		group := byMetric[name]
		m := perf.TargetQualityMetricStats{
			Metric:            name,
			Target:            float64(group[0].Target),
			Tolerance:         float64(group[0].Tolerance),
			Chunks:            len(group),
			StopReasons:       make(map[string]int),
			InitialCRFSources: make(map[string]int),
		}
		crfs := make([]float64, 0, len(group))
		var sumScore, sumAbsErr float64
		for i, log := range group {
			score := float64(log.FinalScore)
			if i == 0 || score < m.ScoreMin {
				m.ScoreMin = score
			}
			if i == 0 || score > m.ScoreMax {
				m.ScoreMax = score
			}
			sumScore += score
			sumAbsErr += absFloat(score - float64(log.Target))
			m.Probes += len(log.Probes)
			stop := string(log.StopReason)
			if stop == "" {
				stop = "none"
			}
			m.StopReasons[stop]++
			if log.InitialCRFSource != "" {
				m.InitialCRFSources[log.InitialCRFSource]++
			}
			crfs = append(crfs, float64(log.FinalCRF))
		}
		sort.Float64s(crfs)
		m.FinalCRFMin = crfs[0]
		m.FinalCRFMedian = crfs[len(crfs)/2]
		m.FinalCRFMax = crfs[len(crfs)-1]
		m.ScoreMean = sumScore / float64(len(group))
		m.ScoreMeanAbsError = sumAbsErr / float64(len(group))
		m.ProbesPerChunk = float64(m.Probes) / float64(len(group))
		stats.Metrics = append(stats.Metrics, m)
	}
	return stats
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func logTargetAggregate(logs []chunkTargetLog, verbose func(string)) {
	if verbose == nil || len(logs) == 0 {
		return
	}
	// SSIMU2 runs mix scales: warmup chunks carry JOD scores, the rest
	// SSIMU2 points. Aggregate per metric so the summary stays meaningful.
	byMetric := make(map[string][]chunkTargetLog)
	for _, log := range logs {
		byMetric[log.Metric] = append(byMetric[log.Metric], log)
	}
	if len(byMetric) > 1 {
		keys := make([]string, 0, len(byMetric))
		for key := range byMetric {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			name := key
			if name == "" {
				name = string(quality.MetricCVVDP)
			}
			verbose(fmt.Sprintf("TQ aggregate for %s-scored chunks:", name))
			logTargetAggregate(byMetric[key], verbose)
		}
		return
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].ChunkIdx < logs[j].ChunkIdx })

	var minScore, maxScore, sumScore, sumAbsErr float32
	var probes int
	crfCounts := make(map[float32]int)
	probeCounts := make(map[int]int)
	stopCounts := make(map[quality.StopReason]int)
	sourceCounts := make(map[string]int)
	var maxProbeChunks, rateCappedChunks []int
	var multiProbeLogs []chunkTargetLog
	for i, log := range logs {
		if i == 0 || log.FinalScore < minScore {
			minScore = log.FinalScore
		}
		if i == 0 || log.FinalScore > maxScore {
			maxScore = log.FinalScore
		}
		sumScore += log.FinalScore
		if log.FinalScore > log.Target {
			sumAbsErr += log.FinalScore - log.Target
		} else {
			sumAbsErr += log.Target - log.FinalScore
		}
		probeCount := len(log.Probes)
		probes += probeCount
		probeCounts[probeCount]++
		stopCounts[log.StopReason]++
		if log.InitialCRFSource != "" {
			sourceCounts[log.InitialCRFSource]++
		}
		crfCounts[log.FinalCRF]++
		switch log.StopReason {
		case quality.StopMaxProbes:
			maxProbeChunks = append(maxProbeChunks, log.ChunkIdx)
		case quality.StopRateCapped:
			rateCappedChunks = append(rateCappedChunks, log.ChunkIdx)
		}
		if probeCount >= 3 {
			multiProbeLogs = append(multiProbeLogs, log)
		}
	}
	meanScore := sumScore / float32(len(logs))
	meanErr := sumAbsErr / float32(len(logs))
	commonCRF := logs[0].FinalCRF
	commonCount := 0
	for crf, count := range crfCounts {
		if count > commonCount {
			commonCRF = crf
			commonCount = count
		}
	}
	verbose(fmt.Sprintf("TQ summary chunks=%d probes=%d probes_per_chunk=%.2f score_min=%.4f mean=%.4f max=%.4f mean_abs_error=%.4f common_crf=%s", len(logs), probes, float64(probes)/float64(len(logs)), minScore, meanScore, maxScore, meanErr, quality.FormatCRF(commonCRF)))
	verbose(fmt.Sprintf("TQ decisions stops=%s probe_counts=%s initial_sources=%s", formatStopCounts(stopCounts), formatIntCounts(probeCounts), formatStringCounts(sourceCounts)))
	if len(multiProbeLogs) > 0 {
		verbose(fmt.Sprintf("TQ multi-probe chunks: %s", formatMultiProbeChunks(multiProbeLogs, 8)))
	}
	if len(maxProbeChunks) > 0 {
		verbose(fmt.Sprintf("TQ max-probe chunks: %s", formatChunkList(maxProbeChunks, 12)))
	}
	if len(rateCappedChunks) > 0 {
		verbose(fmt.Sprintf("TQ rate-capped chunks (bitstream cap bound the search; intended, not a search failure): %s", formatChunkList(rateCappedChunks, 12)))
	}
}

func formatIntCounts(counts map[int]int) string {
	if len(counts) == 0 {
		return "{}"
	}
	keys := make([]int, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%d:%d", key, counts[key]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func formatStringCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", key, counts[key]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func formatMultiProbeChunks(logs []chunkTargetLog, limit int) string {
	if len(logs) == 0 {
		return "[]"
	}
	logs = append([]chunkTargetLog(nil), logs...)
	sort.Slice(logs, func(i, j int) bool {
		if len(logs[i].Probes) != len(logs[j].Probes) {
			return len(logs[i].Probes) > len(logs[j].Probes)
		}
		return logs[i].ChunkIdx < logs[j].ChunkIdx
	})
	if limit <= 0 || limit > len(logs) {
		limit = len(logs)
	}
	parts := make([]string, 0, limit+1)
	for _, log := range logs[:limit] {
		first := log.Probes[0]
		last := log.Probes[len(log.Probes)-1]
		parts = append(parts, fmt.Sprintf("%04d:%d probes crf %s->%s score %.4f->%.4f stop=%s", log.ChunkIdx, len(log.Probes), quality.FormatCRF(first.CRF), quality.FormatCRF(last.CRF), first.Score, last.Score, log.StopReason))
	}
	if limit < len(logs) {
		parts = append(parts, fmt.Sprintf("+%d more", len(logs)-limit))
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func formatStopCounts(counts map[quality.StopReason]int) string {
	stringsCounts := make(map[string]int, len(counts))
	for reason, count := range counts {
		key := string(reason)
		if key == "" {
			key = "none"
		}
		stringsCounts[key] = count
	}
	return formatStringCounts(stringsCounts)
}

func formatChunkList(chunks []int, limit int) string {
	if len(chunks) == 0 {
		return "[]"
	}
	sort.Ints(chunks)
	if limit <= 0 || limit > len(chunks) {
		limit = len(chunks)
	}
	parts := make([]string, 0, limit+1)
	for _, chunkIdx := range chunks[:limit] {
		parts = append(parts, fmt.Sprintf("%04d", chunkIdx))
	}
	if limit < len(chunks) {
		parts = append(parts, fmt.Sprintf("+%d more", len(chunks)-limit))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func writeChunkTargetLog(workDir string, log chunkTargetLog) error {
	path := filepath.Join(workDir, "tq", fmt.Sprintf("%04d.json", log.ChunkIdx))
	data, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func writeAggregateTargetLog(workDir string, logs []chunkTargetLog, tq TargetQualityConfig, calibration *ssimu2Calibration) {
	sort.Slice(logs, func(i, j int) bool { return logs[i].ChunkIdx < logs[j].ChunkIdx })
	metric := tq.Metric
	if metric == "" {
		metric = quality.MetricCVVDP
	}
	var calibrationOffset *float32
	if calibration != nil {
		if offset, locked := calibration.Offset(); locked {
			calibrationOffset = &offset
		}
	}
	data, err := json.MarshalIndent(struct {
		Metric            quality.MetricKind        `json:"metric"`
		Target            float32                   `json:"target"`
		Tolerance         float32                   `json:"tolerance"`
		CalibrationOffset *float32                  `json:"ssimu2_calibration_offset,omitempty"`
		CRFMin            float32                   `json:"crf_min"`
		CRFMax            float32                   `json:"crf_max"`
		MetricWorkers     int                       `json:"metric_workers"`
		DefaultInitialCRF float32                   `json:"default_initial_crf"`
		GrainTreatment    *perf.GrainTreatmentStats `json:"grain_treatment,omitempty"`
		Chunks            []chunkTargetLog          `json:"chunks"`
	}{
		Metric:            metric,
		Target:            tq.Target,
		Tolerance:         tq.Tolerance,
		CalibrationOffset: calibrationOffset,
		CRFMin:            tq.CRFMin,
		CRFMax:            tq.CRFMax,
		MetricWorkers:     tq.MetricWorkers,
		DefaultInitialCRF: tq.InitialCRF,
		GrainTreatment:    tq.GrainTreatment,
		Chunks:            logs,
	}, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')
	_ = os.WriteFile(filepath.Join(workDir, "target-quality.json"), data, 0644)
}
