package audio

import (
	"sort"
	"strconv"
	"strings"

	"spindle/internal/media/ffprobe"
)

// Selection describes the desired audio layout within a ripped container.
type Selection struct {
	Primary        ffprobe.Stream
	PrimaryIndex   int
	KeepIndices    []int
	RemovedIndices []int
}

// PrimaryLabel returns a human-readable summary of the selected primary stream.
func (s Selection) PrimaryLabel() string {
	if s.PrimaryIndex < 0 {
		return ""
	}
	return formatStreamSummary(s.Primary)
}

// Changed reports whether the selection removes any audio streams compared to the source.
func (s Selection) Changed(totalAudio int) bool {
	if totalAudio <= 0 {
		return false
	}
	return len(s.KeepIndices) < totalAudio || len(s.RemovedIndices) > 0
}

// Select returns the audio stream layout that preserves a single primary English track
// only. The function prioritizes tracks by channel count first
// (8ch > 6ch > 2ch), then source quality (lossless > lossy). Spatial audio metadata
// (Atmos, DTS:X) is not prioritized since it's stripped during Opus transcoding.
func Select(streams []ffprobe.Stream) Selection {
	candidates := buildCandidates(streams)
	if len(candidates) == 0 {
		return Selection{PrimaryIndex: -1}
	}

	english := candidates.english()
	if len(english) == 0 {
		// No English audio found; fall back to the first available audio stream.
		english = candidateList{candidates[0]}
	}

	primary := choosePrimary(english)
	selection := Selection{
		Primary:      primary.stream,
		PrimaryIndex: primary.stream.Index,
		KeepIndices:  []int{primary.stream.Index},
	}

	kept := make(map[int]struct{}, len(selection.KeepIndices))
	for _, idx := range selection.KeepIndices {
		kept[idx] = struct{}{}
	}

	removed := make([]int, 0)
	for _, cand := range candidates {
		if _, ok := kept[cand.stream.Index]; ok {
			continue
		}
		removed = append(removed, cand.stream.Index)
	}
	sort.Ints(removed)
	selection.RemovedIndices = removed
	return selection
}

// candidate captures the derived metadata used for audio ranking.
type candidate struct {
	stream         ffprobe.Stream
	order          int
	language       string
	title          string
	isEnglish      bool
	isSpatial      bool
	isLossless     bool
	channels       int
	defaultFlagged bool
}

type candidateList []candidate

func (c candidateList) english() candidateList {
	result := make(candidateList, 0, len(c))
	for _, cand := range c {
		if cand.isEnglish {
			result = append(result, cand)
		}
	}
	return result
}

func choosePrimary(candidates candidateList) candidate {
	if len(candidates) == 0 {
		return candidate{}
	}
	best := candidates[0]
	bestScore := scorePrimary(best)
	for i := 1; i < len(candidates); i++ {
		score := scorePrimary(candidates[i])
		if score > bestScore {
			best = candidates[i]
			bestScore = score
		}
	}
	return best
}

func scorePrimary(cand candidate) float64 {
	score := 0.0

	// Channel count is most important for Opus transcoding.
	// More channels preserved = better output quality.
	switch {
	case cand.channels >= 8:
		score += 1000
	case cand.channels >= 6:
		score += 800
	case cand.channels >= 4:
		score += 600
	case cand.channels >= 2:
		score += 400
	default:
		score += 200
	}

	// Source quality matters for transcoding quality.
	// Lossless source = cleaner transcode to Opus.
	if cand.isLossless {
		score += 100
	} else {
		score += 50
	}

	// Spatial audio metadata (Atmos, DTS:X) is stripped during Opus transcoding,
	// so we don't prioritize it. The channel count already captures 7.1/5.1 layout.

	if cand.defaultFlagged {
		score += 5
	}

	// Prefer earlier tracks when scores tie.
	score -= float64(cand.order) * 0.1

	return score
}

func buildCandidates(streams []ffprobe.Stream) candidateList {
	result := make(candidateList, 0)
	order := 0
	for _, stream := range streams {
		if !strings.EqualFold(stream.CodecType, "audio") {
			continue
		}
		cand := candidate{
			stream:         stream,
			order:          order,
			language:       normalizeLanguage(stream.Tags),
			title:          normalizeTitle(stream.Tags),
			channels:       channelCount(stream),
			defaultFlagged: stream.Disposition != nil && stream.Disposition["default"] == 1,
		}
		cand.isEnglish = strings.HasPrefix(cand.language, "en")
		cand.isSpatial = detectSpatial(stream, cand.title)
		cand.isLossless = detectLossless(stream)
		result = append(result, cand)
		order++
	}
	return result
}

func normalizeLanguage(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	for _, key := range []string{"language", "LANGUAGE", "Language", "language_ietf", "LANG"} {
		if value, ok := tags[key]; ok {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func normalizeTitle(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	for _, key := range []string{"title", "TITLE", "handler_name", "HANDLER_NAME"} {
		if value, ok := tags[key]; ok {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func channelCount(stream ffprobe.Stream) int {
	if stream.Channels > 0 {
		return stream.Channels
	}
	layout := strings.ToLower(strings.TrimSpace(stream.ChannelLayout))
	if layout == "" {
		return 0
	}
	if strings.HasPrefix(layout, "7.1") {
		return 8
	}
	if strings.HasPrefix(layout, "6.1") {
		return 7
	}
	if strings.HasPrefix(layout, "5.1") {
		return 6
	}
	if strings.HasPrefix(layout, "4.0") {
		return 4
	}
	if strings.HasPrefix(layout, "2.1") {
		return 3
	}
	if strings.HasPrefix(layout, "2.0") {
		return 2
	}
	if strings.HasPrefix(layout, "1.0") {
		return 1
	}
	if strings.Contains(layout, ".") {
		parts := strings.Split(layout, ".")
		total := 0
		for _, part := range parts {
			part = strings.Trim(part, "abcdefghijklmnopqrstuvwxyz ()")
			if part == "" {
				continue
			}
			if n, err := strconv.Atoi(part); err == nil {
				total += n
			}
		}
		if total > 0 {
			return total
		}
	}
	return 0
}

func detectSpatial(stream ffprobe.Stream, normalizedTitle string) bool {
	combined := strings.ToLower(strings.Join([]string{
		stream.CodecLong,
		stream.Profile,
		stream.CodecName,
		normalizedTitle,
	}, " "))
	spatialKeywords := []string{
		"atmos",
		"dts:x",
		"dtsx",
		"dts-x",
		"auro-3d",
		"imax enhanced",
	}
	for _, keyword := range spatialKeywords {
		if strings.Contains(combined, keyword) {
			return true
		}
	}
	return false
}

func detectLossless(stream ffprobe.Stream) bool {
	name := strings.ToLower(stream.CodecName)
	long := strings.ToLower(stream.CodecLong)
	switch name {
	case "truehd", "flac", "mlp", "alac", "pcm_s16le", "pcm_s24le", "pcm_s32le", "pcm_bluray", "pcm_s24be", "pcm_s16be":
		return true
	}
	if strings.Contains(long, "lossless") {
		return true
	}
	if strings.Contains(long, "master audio") || strings.Contains(long, "dts-hd") {
		return true
	}
	return false
}

func formatStreamSummary(stream ffprobe.Stream) string {
	parts := make([]string, 0, 4)
	lang := ""
	if stream.Tags != nil {
		lang = strings.TrimSpace(stream.Tags["language"])
		if lang == "" {
			lang = strings.TrimSpace(stream.Tags["LANGUAGE"])
		}
	}
	if lang != "" {
		parts = append(parts, strings.ToLower(lang))
	}
	codec := stream.CodecLong
	if codec == "" {
		codec = stream.CodecName
	}
	if codec != "" {
		parts = append(parts, codec)
	}
	if stream.Channels > 0 {
		parts = append(parts, strconv.Itoa(stream.Channels)+"ch")
	}
	title := ""
	if stream.Tags != nil {
		title = stream.Tags["title"]
	}
	if title != "" {
		parts = append(parts, strings.TrimSpace(title))
	}
	if len(parts) == 0 {
		return "audio"
	}
	return strings.Join(parts, " | ")
}
