// Package audio provides audio track selection for the Spindle media pipeline.
//
// It selects the single primary English audio track for ripping by scoring
// candidates on channel count, lossless codec, and default flag.
package audio

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/language"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/ffprobe"
)

// candidate holds scoring data for a single audio stream.
type candidate struct {
	stream     ffprobe.Stream
	index      int
	language   string
	channels   int
	isSpatial  bool
	isLossless bool
	isDefault  bool
	score      float64
}

// Selection holds the result of audio track selection.
type Selection struct {
	Primary        ffprobe.Stream
	PrimaryIndex   int
	KeepIndices    []int
	RemovedIndices []int
}

// PrimaryLabel returns a human-readable summary of the primary track:
// "language | codec | channels | title".
func (s Selection) PrimaryLabel() string {
	lang := language.DisplayName(language.ExtractFromTags(s.Primary.Tags))
	codec := s.Primary.CodecName
	ch := s.Primary.Channels
	title := s.Primary.Tags["title"]

	label := fmt.Sprintf("%s | %s | %dch", lang, codec, ch)
	if title != "" {
		label += " | " + title
	}
	return label
}

// Changed reports whether any audio streams are removed compared to the
// total audio stream count.
func (s Selection) Changed(totalAudio int) bool {
	return len(s.RemovedIndices) > 0 || totalAudio != len(s.KeepIndices)
}

// Select implements the audio track selection algorithm. It picks the single
// best English audio track from the provided streams. Non-audio streams are
// ignored. If no English track is found, the first audio stream is used as
// a fallback.
func Select(logger *slog.Logger, streams []ffprobe.Stream) Selection {
	logger = logs.Default(logger)

	// Build candidate list from audio streams only.
	var candidates []candidate
	for i, st := range streams {
		if st.CodecType != "audio" {
			continue
		}
		lang := language.ToISO2(language.ExtractFromTags(st.Tags))
		c := candidate{
			stream:     st,
			index:      i,
			language:   lang,
			channels:   parseChannelCount(st),
			isSpatial:  isSpatialAudio(st),
			isLossless: isLosslessCodec(st),
			isDefault:  st.Disposition["default"] == 1,
		}
		candidates = append(candidates, c)
	}

	if len(candidates) == 0 {
		return Selection{}
	}

	// Filter to English candidates.
	var english []candidate
	for _, c := range candidates {
		if strings.HasPrefix(c.language, "en") {
			english = append(english, c)
		}
	}

	pool := english
	fallback := len(pool) == 0
	if fallback {
		// Fall back to first available audio stream.
		pool = candidates[:1]
		logger.Info("audio selection fallback to non-english",
			"decision_type", logs.DecisionAudioSelection,
			"decision_result", "fallback_non_english",
			"decision_reason", fmt.Sprintf("no english audio among %d candidates", len(candidates)),
			"fallback_language", pool[0].language,
		)
	}

	// Score each candidate in the pool.
	for i := range pool {
		pool[i].score = scoreCandidate(pool[i], i)
		logger.Debug("audio candidate scored",
			"index", pool[i].index,
			"language", pool[i].language,
			"channels", pool[i].channels,
			"lossless", pool[i].isLossless,
			"score", pool[i].score,
		)
	}

	// Select the highest-scoring candidate.
	best := 0
	for i := 1; i < len(pool); i++ {
		if pool[i].score > pool[best].score {
			best = i
		}
	}
	primary := pool[best]

	// Build keep/removed index lists.
	sel := Selection{
		Primary:      primary.stream,
		PrimaryIndex: primary.index,
		KeepIndices:  []int{primary.index},
	}
	for _, c := range candidates {
		if c.index != primary.index {
			sel.RemovedIndices = append(sel.RemovedIndices, c.index)
		}
	}

	logger.Info("audio track selected",
		"decision_type", logs.DecisionAudioSelection,
		"decision_result", "selected",
		"decision_reason", sel.PrimaryLabel(),
		"candidates", len(candidates),
		"primary_index", primary.index,
		"primary_score", primary.score,
	)

	return sel
}

// scoreCandidate computes a score for an audio track candidate.
// Higher is better.
func scoreCandidate(c candidate, position int) float64 {
	var score float64

	// Channel count score.
	switch {
	case c.channels >= 8:
		score = 1000
	case c.channels >= 6:
		score = 800
	case c.channels >= 4:
		score = 600
	default:
		score = 400
	}

	// Lossless bonus.
	if c.isLossless {
		score += 100
	}

	// Default flag bonus.
	if c.isDefault {
		score += 5
	}

	// Stream order tiebreaker (earlier streams slightly preferred).
	score -= 0.1 * float64(position)

	return score
}

// spatialKeywords are patterns that indicate spatial/immersive audio formats.
var spatialKeywords = []string{
	"atmos",
	"dts:x",
	"dtsx",
	"dts-x",
	"auro-3d",
	"imax enhanced",
}

// isSpatialAudio checks whether a stream uses a spatial audio format.
func isSpatialAudio(s ffprobe.Stream) bool {
	fields := []string{
		s.CodecLong,
		s.Profile,
		s.CodecName,
		s.Tags["title"],
	}
	for _, field := range fields {
		lower := strings.ToLower(field)
		for _, kw := range spatialKeywords {
			if strings.Contains(lower, kw) {
				return true
			}
		}
	}
	return false
}

// losslessCodecs is the set of codec names considered lossless.
var losslessCodecs = map[string]bool{
	"truehd":     true,
	"flac":       true,
	"mlp":        true,
	"alac":       true,
	"pcm_s16le":  true,
	"pcm_s24le":  true,
	"pcm_s32le":  true,
	"pcm_bluray": true,
	"pcm_s24be":  true,
	"pcm_s16be":  true,
}

// losslessLongNameKeywords are patterns in codec_long_name that indicate lossless.
var losslessLongNameKeywords = []string{
	"lossless",
	"master audio",
	"dts-hd",
}

// isLosslessCodec checks whether a stream uses a lossless audio codec.
func isLosslessCodec(s ffprobe.Stream) bool {
	if losslessCodecs[strings.ToLower(s.CodecName)] {
		return true
	}
	lower := strings.ToLower(s.CodecLong)
	for _, kw := range losslessLongNameKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// parseChannelCount extracts the number of audio channels from a stream.
// It prefers the Channels field; if zero, it parses ChannelLayout.
func parseChannelCount(s ffprobe.Stream) int {
	if s.Channels > 0 {
		return s.Channels
	}
	return parseLayoutChannels(s.ChannelLayout)
}

// parseLayoutChannels parses a channel layout string into a channel count.
// Examples: "7.1" -> 8, "5.1(side)" -> 6, "stereo" -> 2, "mono" -> 1.
func parseLayoutChannels(layout string) int {
	layout = strings.ToLower(strings.TrimSpace(layout))
	if layout == "" {
		return 0
	}

	switch layout {
	case "mono":
		return 1
	case "stereo":
		return 2
	}

	// Strip parenthetical suffixes like "(side)".
	if idx := strings.Index(layout, "("); idx >= 0 {
		layout = layout[:idx]
	}

	// Parse "X.Y" format.
	parts := strings.SplitN(layout, ".", 2)
	if len(parts) == 2 {
		main, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		sub, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 == nil && err2 == nil {
			return main + sub
		}
	}

	return 0
}
