package audio

import (
	"sort"
	"strconv"
	"strings"

	"spindle/internal/media/ffprobe"
)

// Selection describes the desired audio layout within a ripped container.
type Selection struct {
	Primary           ffprobe.Stream
	PrimaryIndex      int
	Commentary        []ffprobe.Stream
	CommentaryIndices []int
	KeepIndices       []int
	RemovedIndices    []int
}

// PrimaryLabel returns a human-readable summary of the selected primary stream.
func (s Selection) PrimaryLabel() string {
	if s.PrimaryIndex < 0 {
		return ""
	}
	return formatStreamSummary(s.Primary)
}

// CommentaryLabels returns formatted summaries for commentary selections.
func (s Selection) CommentaryLabels() []string {
	if len(s.Commentary) == 0 {
		return nil
	}
	labels := make([]string, 0, len(s.Commentary))
	for _, stream := range s.Commentary {
		labels = append(labels, formatStreamSummary(stream))
	}
	return labels
}

// Changed reports whether the selection removes any audio streams compared to the source.
func (s Selection) Changed(totalAudio int) bool {
	if totalAudio <= 0 {
		return false
	}
	return len(s.KeepIndices) < totalAudio || len(s.RemovedIndices) > 0
}

// Select returns the audio stream layout that preserves a single primary English track
// alongside any commentary tracks. The function prefers spatial mixes, then lossless,
// and finally the best lossy option when no higher-fidelity stream exists.
func Select(streams []ffprobe.Stream) Selection {
	candidates := buildCandidates(streams)
	if len(candidates) == 0 {
		return Selection{PrimaryIndex: -1}
	}

	english := candidates.english()
	mainCandidates := english.nonCommentary()

	if len(mainCandidates) == 0 {
		// Fall back to any English audio even if flagged as commentary.
		mainCandidates = english
	}
	if len(mainCandidates) == 0 {
		// No English audio found; fall back to the first available audio stream.
		mainCandidates = candidateList{candidates[0]}
	}

	primary := choosePrimary(mainCandidates)
	selection := Selection{
		Primary:      primary.stream,
		PrimaryIndex: primary.stream.Index,
		KeepIndices:  []int{primary.stream.Index},
	}

	hasEnglishMultichannel := english.hasMultiChannel()
	commentaries := english.commentaryCandidates(primary, hasEnglishMultichannel)
	if len(commentaries) > 0 {
		selection.Commentary = make([]ffprobe.Stream, 0, len(commentaries))
		selection.CommentaryIndices = make([]int, 0, len(commentaries))
		for _, cand := range commentaries {
			selection.Commentary = append(selection.Commentary, cand.stream)
			selection.CommentaryIndices = append(selection.CommentaryIndices, cand.stream.Index)
			selection.KeepIndices = append(selection.KeepIndices, cand.stream.Index)
		}
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
	isCommentary   bool
	isDescriptive  bool
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

func (c candidateList) nonCommentary() candidateList {
	result := make(candidateList, 0, len(c))
	for _, cand := range c {
		if !cand.isCommentary && !cand.isDescriptive {
			result = append(result, cand)
		}
	}
	return result
}

func (c candidateList) hasMultiChannel() bool {
	for _, cand := range c {
		if cand.channels > 2 {
			return true
		}
	}
	return false
}

func (c candidateList) commentaryCandidates(primary candidate, hasEnglishMultichannel bool) candidateList {
	result := make(candidateList, 0)

	// Count stereo English tracks to help identify commentary scenarios
	stereoCount := 0
	for _, cand := range c {
		if cand.channels <= 2 {
			stereoCount++
		}
	}

	for _, cand := range c {
		if cand.stream.Index == primary.stream.Index {
			continue
		}
		if cand.isCommentary {
			result = append(result, cand)
			continue
		}
		if cand.isDescriptive {
			continue
		}

		// More conservative stereo heuristic: only treat stereo as commentary if:
		// 1. Multichannel audio exists AND
		// 2. Either the title suggests commentary OR there are multiple stereo tracks
		if hasEnglishMultichannel && cand.channels <= 2 {
			// Check if title has any commentary-suggesting terms
			hasSuggestiveTitle := titleSuggestsCommentary(cand.title)

			// If multiple stereo tracks exist alongside multichannel, they're likely alternatives
			multipleAlternatives := stereoCount > 1

			if hasSuggestiveTitle || multipleAlternatives {
				result = append(result, cand)
			}
		}
	}
	// Preserve original order for deterministic output.
	sort.SliceStable(result, func(i, j int) bool { return result[i].order < result[j].order })
	return dedupeCandidates(result)
}

func dedupeCandidates(list candidateList) candidateList {
	seen := make(map[int]struct{}, len(list))
	result := make(candidateList, 0, len(list))
	for _, cand := range list {
		if _, ok := seen[cand.stream.Index]; ok {
			continue
		}
		seen[cand.stream.Index] = struct{}{}
		result = append(result, cand)
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
	if cand.isSpatial {
		score += 1000
	} else if cand.isLossless {
		score += 800
	} else {
		score += 500
	}

	switch {
	case cand.channels >= 8:
		score += 80
	case cand.channels >= 6:
		score += 60
	case cand.channels >= 4:
		score += 40
	case cand.channels >= 2:
		score += 20
	default:
		score += 10
	}

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
		cand.isCommentary = detectCommentary(stream, cand.title)
		cand.isDescriptive = detectDescriptive(stream, cand.title)
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

func detectCommentary(stream ffprobe.Stream, normalizedTitle string) bool {
	if stream.Disposition != nil {
		if stream.Disposition["dub"] == 1 {
			return false
		}
		if stream.Disposition["original"] == 1 {
			return false
		}
		if stream.Disposition["commentary"] == 1 {
			return true
		}
	}

	texts := gatherCommentaryText(stream, normalizedTitle)
	if len(texts) == 0 {
		return false
	}
	for _, text := range texts {
		if containsAny(text, directCommentaryKeywords) {
			return true
		}
		if commentaryContextMatch(text) {
			return true
		}
	}
	return false
}

var directCommentaryKeywords = []string{
	"commentary",
	"commentaries",
	"audio commentary",
	"feature commentary",
	"commentary track",
	"talk track",
	"commentary w/",
	"in conversation",
	"conversation",
	"roundtable",
	"q&a",
	"qa",
	"panel",
	"discussion",
	"chat track",
	"interview",
}

var commentaryRoleKeywords = []string{
	"director",
	"directors",
	"producer",
	"producers",
	"writer",
	"writers",
	"screenwriter",
	"cast",
	"crew",
	"filmmaker",
	"filmmakers",
	"actor",
	"actors",
	"dp",
}

var commentaryContextKeywords = []string{
	"discussion",
	"conversation",
	"talk",
	"roundtable",
	"panel",
	"q&a",
	"qa",
	"interview",
	"commentary",
}

func commentaryContextMatch(text string) bool {
	if !containsAny(text, commentaryContextKeywords) {
		return false
	}
	return containsAny(text, commentaryRoleKeywords)
}

func containsAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if keyword == "" {
			continue
		}
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func gatherCommentaryText(stream ffprobe.Stream, normalizedTitle string) []string {
	seen := make(map[string]struct{})
	texts := make([]string, 0, 4)
	add := func(value string) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		texts = append(texts, value)
	}
	add(normalizedTitle)
	if stream.Tags == nil {
		return texts
	}
	for _, key := range []string{"comment", "COMMENT", "comments", "COMMENTS", "description", "DESCRIPTION", "handler_name", "HANDLER_NAME"} {
		if value, ok := stream.Tags[key]; ok {
			add(value)
		}
	}
	return texts
}

// titleSuggestsCommentary checks if a title contains hints that it might be commentary,
// even if not definitively flagged. Used as a secondary signal for stereo track classification.
func titleSuggestsCommentary(normalizedTitle string) bool {
	if normalizedTitle == "" {
		return false
	}
	// Weaker indicators reserved for stereo/mono reclassification.
	softHints := []string{
		"commentary",
		"discussion",
		"talk",
		"roundtable",
		"q&a",
		"qa",
		"interview",
		"bonus commentary",
	}
	for _, hint := range softHints {
		if strings.Contains(normalizedTitle, hint) {
			return true
		}
	}

	// Treat explicit stereo/mono mix labels as commentary-style alternates when
	// multichannel audio is also present.
	if strings.Contains(normalizedTitle, "stereo") || strings.Contains(normalizedTitle, "mono") {
		if strings.Contains(normalizedTitle, "mix") || strings.Contains(normalizedTitle, "track") ||
			strings.Contains(normalizedTitle, "downmix") || strings.Contains(normalizedTitle, "fold") {
			return true
		}
	}
	return false
}

func detectDescriptive(stream ffprobe.Stream, normalizedTitle string) bool {
	if stream.Disposition != nil {
		if stream.Disposition["hearing_impaired"] == 1 {
			return true
		}
		if stream.Disposition["visual_impaired"] == 1 {
			return true
		}
	}
	if normalizedTitle == "" {
		return false
	}
	descriptors := []string{
		"descriptive",
		"description",
		"audio description",
		"narration",
		"described",
		"visually",
		"dvs",
		"described video",
	}
	for _, word := range descriptors {
		if strings.Contains(normalizedTitle, word) {
			return true
		}
	}
	return false
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
