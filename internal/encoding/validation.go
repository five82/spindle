package encoding

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"spindle/internal/encodingstate"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
)

// ValidateEpisodeConsistency probes all encoded TV episode files and compares
// their media profiles (video codec, resolution, audio stream count). Deviations
// from the majority profile flag the item for review. Called after audio analysis
// so audio stream counts reflect the final output, not raw Drapto passthrough.
func ValidateEpisodeConsistency(ctx context.Context, item *queue.Item, env *ripspec.Envelope, logger *slog.Logger) {
	if env == nil || len(env.Episodes) < 2 {
		return
	}

	type profile struct {
		videoCodec     string
		width, height  int
		audioStreamCnt int
	}

	type entry struct {
		key string
		p   profile
	}

	var entries []entry
	for _, asset := range env.Assets.Encoded {
		if asset.IsFailed() || strings.TrimSpace(asset.Path) == "" {
			continue
		}

		result, err := encodeProbe(ctx, "", asset.Path)
		if err != nil {
			logger.Warn("probe failed during episode consistency check",
				logging.String("episode_key", asset.EpisodeKey),
				logging.String("path", asset.Path),
				logging.Error(err),
				logging.String(logging.FieldEventType, "episode_consistency_probe_failed"),
				logging.String(logging.FieldErrorHint, "encoded file may be corrupt or missing"),
				logging.String(logging.FieldImpact, "episode excluded from consistency check"),
			)
			continue
		}

		var p profile
		for _, s := range result.Streams {
			switch strings.ToLower(s.CodecType) {
			case "video":
				p.videoCodec = s.CodecName
				p.width = s.Width
				p.height = s.Height
			case "audio":
				p.audioStreamCnt++
			}
		}
		entries = append(entries, entry{key: asset.EpisodeKey, p: p})
	}

	if len(entries) < 2 {
		return
	}

	// Find majority profile.
	type profileCount struct {
		p     profile
		count int
	}
	var counts []profileCount
	for _, e := range entries {
		found := false
		for i := range counts {
			if counts[i].p == e.p {
				counts[i].count++
				found = true
				break
			}
		}
		if !found {
			counts = append(counts, profileCount{p: e.p, count: 1})
		}
	}
	best := 0
	for i, c := range counts {
		if c.count > counts[best].count {
			best = i
		}
	}
	majority := counts[best].p

	// Find deviations.
	var deviatingKeys []string
	for _, e := range entries {
		if e.p == majority {
			continue
		}
		var diffs []string
		if e.p.videoCodec != majority.videoCodec {
			diffs = append(diffs, fmt.Sprintf("codec %s vs %s", e.p.videoCodec, majority.videoCodec))
		}
		if e.p.width != majority.width || e.p.height != majority.height {
			diffs = append(diffs, fmt.Sprintf("resolution %dx%d vs %dx%d", e.p.width, e.p.height, majority.width, majority.height))
		}
		if e.p.audioStreamCnt != majority.audioStreamCnt {
			diffs = append(diffs, fmt.Sprintf("audio streams %d vs %d", e.p.audioStreamCnt, majority.audioStreamCnt))
		}
		if len(diffs) > 0 {
			deviatingKeys = append(deviatingKeys, e.key)
			logger.Warn("episode media profile deviates from majority",
				logging.String("episode_key", e.key),
				logging.String("differences", strings.Join(diffs, "; ")),
				logging.String(logging.FieldEventType, "episode_consistency_deviation"),
				logging.String(logging.FieldErrorHint, "verify encoded episodes have consistent settings"),
				logging.String(logging.FieldImpact, "episode may appear different from others in the season"),
			)
		}
	}

	if len(deviatingKeys) > 0 {
		item.NeedsReview = true
		if item.ReviewReason == "" {
			item.ReviewReason = fmt.Sprintf("%d episode(s) deviate from majority media profile", len(deviatingKeys))
		}
		attrs := append(logging.DecisionAttrs("episode_consistency", "needs_review", "profile_deviation"),
			logging.Int("deviating_episodes", len(deviatingKeys)),
			logging.Int("total_episodes", len(entries)),
		)
		logger.Info("episode consistency decision", logging.Args(attrs...)...)
	} else {
		attrs := append(logging.DecisionAttrs("episode_consistency", "consistent", "all_profiles_match"),
			logging.Int("total_episodes", len(entries)),
		)
		logger.Info("episode consistency decision", logging.Args(attrs...)...)
	}
}

// validateCropRatio parses the crop filter from the encoding snapshot, computes
// the aspect ratio, and logs whether it matches a standard ratio. Movies only.
func validateCropRatio(snapshot *encodingstate.Snapshot, logger *slog.Logger) {
	if snapshot == nil || snapshot.Crop == nil {
		return
	}

	filter := snapshot.Crop.Crop
	w, h, ok := encodingstate.ParseCropFilter(filter)
	if !ok || h == 0 {
		return
	}

	ratio := math.Round(float64(w)/float64(h)*100) / 100
	standardName := encodingstate.MatchStandardRatio(ratio)

	attrs := append(logging.DecisionAttrs("crop_aspect_ratio", standardName, fmt.Sprintf("crop %dx%d = %.2f:1", w, h, ratio)),
		logging.String("crop_filter", filter),
	)
	logger.Info("crop aspect ratio decision", logging.Args(attrs...)...)
}
