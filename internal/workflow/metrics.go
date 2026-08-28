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
// completes. It is the durable cross-item performance record: the queue DB is
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
	Schema           int                   `json:"schema"`
	CompletedAt      time.Time             `json:"completed_at"`
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
	SpindleVersion   string                `json:"spindle_version,omitempty"`
	ReelVersion      string                `json:"reel_version,omitempty"`
}

// writeMetricsRecord appends the completed item's metrics line. Best-effort:
// metrics must never affect pipeline outcomes, so failures only warn.
func (m *Manager) writeMetricsRecord(item *queue.Item, tasks []*queue.Task) {
	if m.metricsPath == "" {
		return
	}
	rec := metricsRecord{
		Schema:          1,
		CompletedAt:     time.Now().UTC(),
		ItemID:          item.ID,
		Title:           item.DisplayTitle(),
		DiscFingerprint: item.DiscFingerprint,
		NeedsReview:     item.NeedsReview == 1,
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
	rec.SpindleVersion, rec.ReelVersion = buildVersions()

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
		"impact", "completed item missing from metrics log",
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

var versionOnce = sync.OnceValues(func() (string, string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", ""
	}
	spindle := info.Main.Version
	reel := ""
	for _, dep := range info.Deps {
		if dep.Path == "github.com/five82/reel" {
			reel = dep.Version
			if dep.Replace != nil {
				reel = dep.Replace.Version
			}
			break
		}
	}
	return spindle, reel
})

func buildVersions() (spindleVersion, reelVersion string) {
	return versionOnce()
}
