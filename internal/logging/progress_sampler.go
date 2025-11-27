package logging

import "strings"

// ProgressSampler suppresses repetitive progress logs while preserving signal
// when stages, messages, or percentage buckets change.
type ProgressSampler struct {
	bucketSize float64
	lastStage  string
	lastMsg    string
	lastBucket int
}

// NewProgressSampler constructs a sampler that emits when the percent crosses
// bucket boundaries (default 5%), when the stage changes, or when the message
// changes.
func NewProgressSampler(bucketSize float64) *ProgressSampler {
	if bucketSize <= 0 {
		bucketSize = 5
	}
	return &ProgressSampler{bucketSize: bucketSize, lastBucket: -1}
}

// ShouldLog reports whether a progress event should be logged. Percent can be
// negative to indicate "unknown"; stage and message are trimmed before
// comparison.
func (s *ProgressSampler) ShouldLog(percent float64, stage, message string) bool {
	if s == nil {
		return true
	}
	stage = strings.TrimSpace(stage)
	message = strings.TrimSpace(message)
	emit := false
	if stage != "" && stage != s.lastStage {
		s.lastStage = stage
		emit = true
		s.lastBucket = -1
	}
	if message != "" && message != s.lastMsg {
		s.lastMsg = message
		emit = true
	}
	if percent >= 0 {
		bucket := int(percent / s.bucketSize)
		if percent >= 100 {
			bucket = int(100 / s.bucketSize)
		}
		if bucket > s.lastBucket {
			s.lastBucket = bucket
			emit = true
		}
	}
	return emit
}

// Reset clears the sampler state (e.g. when a new job starts).
func (s *ProgressSampler) Reset() {
	if s == nil {
		return
	}
	s.lastStage = ""
	s.lastMsg = ""
	s.lastBucket = -1
}
