package workflow

import (
	"encoding/json"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

// The metrics log is one JSON object per line, appended when an item
// completes or fails. It is the durable cross-item performance record: the queue DB is
// transient and log files expire, but this file accumulates for trend
// analysis (rip speed per drive, encode speed per resolution class, stage
// bottlenecks). Fields are self-describing so it can be queried directly with
// jq or read by an LLM; new fields may appear over time, records are never
// rewritten.

type metricsStage struct {
	Stage       string  `json:"stage"`
	Seconds     float64 `json:"seconds,omitempty"`
	WaitSeconds float64 `json:"wait_seconds,omitempty"`
}

type metricsRecord struct {
	Schema int `json:"schema"`
	// Outcome is "completed" or "failed". Records written before failures
	// were recorded lack it and are all completions.
	Outcome          string                `json:"outcome"`
	CompletedAt      time.Time             `json:"completed_at,omitzero"`
	FailedAt         time.Time             `json:"failed_at,omitzero"`
	FailedStage      queue.Stage           `json:"failed_stage,omitempty"`
	Error            string                `json:"error,omitempty"`
	ItemID           int64                 `json:"item_id"`
	Title            string                `json:"title"`
	MediaType        string                `json:"media_type,omitempty"`
	DiscType         string                `json:"disc_type,omitempty"`
	DiscFingerprint  string                `json:"disc_fingerprint,omitempty"`
	NeedsReview      bool                  `json:"needs_review,omitempty"`
	RipCached        bool                  `json:"rip_cached,omitempty"`
	TotalWallSeconds float64               `json:"total_wall_seconds,omitempty"`
	Stages           []metricsStage        `json:"stages"`
	Rip              *ripspec.RipStats     `json:"rip,omitempty"`
	Encodes          []ripspec.EncodeStats `json:"encodes,omitempty"`
	Hostname         string                `json:"hostname,omitempty"`
	// SpindleVersion identifies the build, which includes Reel: both live
	// in one module.
	SpindleVersion string `json:"spindle_version,omitempty"`
}

// writeMetricsRecord appends the item's terminal metrics line: a completion
// when failure is nil, otherwise the failure at failedStage, so failed items
// outlive the transient queue and expiring logs too. Best-effort: metrics
// must never affect pipeline outcomes, so write failures only warn.
func (m *Manager) writeMetricsRecord(item *queue.Item, tasks []*queue.Task, failedStage queue.Stage, failure error) {
	if m.metricsPath == "" {
		return
	}
	rec := metricsRecord{
		Schema:          1,
		Outcome:         "completed",
		ItemID:          item.ID,
		Title:           item.DisplayTitle(),
		DiscFingerprint: item.DiscFingerprint,
		NeedsReview:     item.NeedsReview == 1,
	}
	if failure != nil {
		rec.Outcome, rec.FailedAt, rec.FailedStage, rec.Error = "failed", time.Now().UTC(), failedStage, failure.Error()
	} else {
		rec.CompletedAt = time.Now().UTC()
	}
	if created, ok := item.CreatedTime(); ok {
		rec.TotalWallSeconds = time.Since(created).Seconds()
	}
	waits := m.takeWaits(item.ID)
	for _, t := range tasks {
		st := metricsStage{Stage: string(t.Type), WaitSeconds: waits[t.Type]}
		if d, ok := t.Duration(); ok {
			st.Seconds = d.Seconds()
		}
		rec.Stages = append(rec.Stages, st)
	}
	if env, err := ripspec.Parse(item.RipSpecData); err == nil {
		rec.MediaType = env.Metadata.MediaType
		rec.DiscType = env.Metadata.DiscSource
		rec.RipCached = env.Metadata.Cached
		rec.Rip = env.Attributes.Rip
		rec.Encodes = env.Attributes.EncodeStats
	}
	rec.Hostname, _ = os.Hostname()
	rec.SpindleVersion = buildVersion()

	line, err := json.Marshal(rec)
	if err != nil {
		m.warnMetrics(item.ID, err)
		return
	}
	f, err := os.OpenFile(m.metricsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		m.warnMetrics(item.ID, err)
		return
	}
	_, writeErr := f.Write(append(line, '\n'))
	if closeErr := f.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		m.warnMetrics(item.ID, writeErr)
		return
	}
	m.pipeline.logger.Debug("metrics record appended",
		"item_id", item.ID,
		"path", m.metricsPath,
	)
}

func (m *Manager) warnMetrics(itemID int64, err error) {
	m.pipeline.logger.Warn("metrics record not written",
		"event_type", "metrics_write_error",
		"error_hint", err.Error(),
		"impact", "item missing from metrics log",
		"item_id", itemID,
	)
}

// takeWaits removes and returns the accumulated resource-wait seconds for an
// item, so a record consumes its waits exactly once.
func (m *Manager) takeWaits(itemID int64) map[queue.Stage]float64 {
	m.blockedMu.Lock()
	defer m.blockedMu.Unlock()
	waits := m.waits[itemID]
	delete(m.waits, itemID)
	return waits
}

var buildVersion = sync.OnceValue(func() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return info.Main.Version
	}
	return ""
})
