package identify

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/tmdb"
)

// TV title selection keeps every title that could plausibly be an episode and
// leaves final arbitration to content identification. Titles are dropped only
// on structural evidence (length gate, duplicates, gross outliers, proven
// play-all composites) or on a strong mismatch against TMDB expected episode
// runtimes. Intra-disc runtime statistics order titles; they never drop them
// on their own.
const (
	// tvGrossOutlierRatio drops titles shorter than this fraction of the
	// duration-weighted median candidate: menus, trailers, credit reels.
	tvGrossOutlierRatio = 0.4
	// tvDoubleMinRatio/tvDoubleMaxRatio describe a double-length episode
	// relative to the median single episode. Used only to order a probable
	// opening double first (contentid's opening-double inference reads
	// episode order), never to exclude.
	tvDoubleMinRatio = 1.80
	tvDoubleMaxRatio = 2.40
	// tvRuntimeToleranceRatio/tvRuntimeToleranceSec bound how far a title may
	// deviate from the nearest TMDB expected episode runtime (or adjacent
	// double-episode sum) before an expectation-backed disc drops it. The
	// ratio is generous because TMDB runtimes are frequently rounded or
	// slightly wrong for older shows.
	tvRuntimeToleranceRatio = 0.25
	tvRuntimeToleranceSec   = 300
	// tvSameLengthRatio treats two long titles as alternates of the same
	// program when their durations differ by less than this fraction.
	tvSameLengthRatio = 0.10
)

type tvTitleDecision struct {
	Title       ripspec.Title
	Selected    bool
	Reason      string
	DuplicateOf int
}

type tvTitleSelectionResult struct {
	Decisions        []tvTitleDecision
	SelectedTitles   []ripspec.Title
	DuplicateCount   int
	ExtraCount       int
	Ambiguous        bool
	AmbiguityReasons []string
	// Evidence behind the structural exclusions, surfaced for decision logs:
	// the duration-weighted median and outlier bar that drove
	// gross_runtime_outlier, and the TMDB runtime targets (seconds) that drove
	// expected_runtime_mismatch / over_expected_episode_count.
	WeightedMedianSeconds  int
	OutlierBarSeconds      int
	ExpectedRuntimeTargets []int
}

type tvTitleCandidate struct {
	decisionIndex int
	title         ripspec.Title
}

type compositeTitle struct {
	candidate  tvTitleCandidate
	components []tvTitleCandidate
}

func selectTVEpisodeTitles(titles []ripspec.Title, minTitleLength int, expected []tmdb.Episode) tvTitleSelectionResult {
	result := tvTitleSelectionResult{Decisions: make([]tvTitleDecision, 0, len(titles))}
	candidates := make([]tvTitleCandidate, 0, len(titles))
	seen := make(map[string]int)

	for _, title := range titles {
		decision := tvTitleDecision{Title: title}
		key := dedupKey(title)
		switch {
		case title.Duration < minTitleLength:
			decision.Reason = "below_min_title_length"
		case key != "":
			if firstID, dup := seen[key]; dup {
				decision.Reason = "duplicate_title"
				decision.DuplicateOf = firstID
				result.DuplicateCount++
			} else {
				seen[key] = title.ID
				decision.Reason = "candidate"
				candidates = append(candidates, tvTitleCandidate{decisionIndex: len(result.Decisions), title: title})
			}
		default:
			decision.Reason = "candidate"
			candidates = append(candidates, tvTitleCandidate{decisionIndex: len(result.Decisions), title: title})
		}
		result.Decisions = append(result.Decisions, decision)
	}

	if len(candidates) == 0 {
		return result
	}

	alive := excludeGrossOutliers(candidates, &result)
	if len(alive) == 1 && len(candidates) > 1 {
		result.Ambiguous = true
		result.AmbiguityReasons = append(result.AmbiguityReasons, "single_episode_length_candidate")
	}
	alive = resolveComposites(alive, &result)
	alive = excludeRuntimeMismatches(alive, expected, &result)
	alive = capToExpectedCount(alive, expected, &result)

	if len(alive) == 0 {
		fallback := longestCandidate(candidates)
		decision := &result.Decisions[fallback.decisionIndex]
		decision.Selected = true
		decision.Reason = "fallback_longest_candidate"
		result.SelectedTitles = append(result.SelectedTitles, decision.Title)
		result.ExtraCount = max(0, result.ExtraCount-1)
		result.Ambiguous = true
		result.AmbiguityReasons = append(result.AmbiguityReasons, "no_episode_candidates")
		return result
	}

	for _, candidate := range alive {
		decision := &result.Decisions[candidate.decisionIndex]
		if !decision.Selected {
			decision.Selected = true
			decision.Reason = "episode_candidate"
		}
	}
	result.SelectedTitles = orderSelectedTitles(alive, result.Decisions)
	return result
}

// excludeGrossOutliers drops candidates shorter than tvGrossOutlierRatio of
// the duration-weighted median. The duration weighting keeps the bar anchored
// to episode-length content even when short extras outnumber episodes.
func excludeGrossOutliers(candidates []tvTitleCandidate, result *tvTitleSelectionResult) []tvTitleCandidate {
	median := durationWeightedMedian(candidates)
	bar := int(float64(median) * tvGrossOutlierRatio)
	result.WeightedMedianSeconds = median
	result.OutlierBarSeconds = bar
	alive := make([]tvTitleCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.title.Duration < bar {
			result.Decisions[candidate.decisionIndex].Reason = "gross_runtime_outlier"
			result.ExtraCount++
			continue
		}
		alive = append(alive, candidate)
	}
	return alive
}

// durationWeightedMedian returns the duration of the candidate at the
// midpoint of cumulative duration mass.
func durationWeightedMedian(candidates []tvTitleCandidate) int {
	durations := make([]int, 0, len(candidates))
	total := 0
	for _, candidate := range candidates {
		durations = append(durations, candidate.title.Duration)
		total += candidate.title.Duration
	}
	slices.Sort(durations)
	cumulative := 0
	for _, duration := range durations {
		cumulative += duration
		if cumulative*2 >= total {
			return duration
		}
	}
	return durations[len(durations)-1]
}

// resolveComposites detects titles whose segment maps are explainable as a
// combination of other candidates. The normal disposition is play-all
// exclusion: the composite is a hidden concatenation of real episodes. The
// exception is a split double episode (e.g. a seamless-branch pilot): when
// the disc has exactly one composite, its components share segments, and no
// independent same-length alternate exists, the combined title is the real
// program and the halves are partial cuts.
func resolveComposites(alive []tvTitleCandidate, result *tvTitleSelectionResult) []tvTitleCandidate {
	composites := detectComposites(alive)
	if len(composites) == 0 {
		return alive
	}

	if len(composites) == 1 {
		composite := composites[0]
		if tvComponentsOverlap(composite.components) && !hasSameLengthAlternate(alive, composite) {
			decision := &result.Decisions[composite.candidate.decisionIndex]
			decision.Selected = true
			decision.Reason = "combined_double_episode_candidate"
			dropped := make(map[int]struct{}, len(composite.components))
			for _, component := range composite.components {
				result.Decisions[component.decisionIndex].Reason = "combined_title_component"
				result.ExtraCount++
				dropped[component.decisionIndex] = struct{}{}
			}
			kept := make([]tvTitleCandidate, 0, len(alive)-len(dropped))
			for _, candidate := range alive {
				if _, ok := dropped[candidate.decisionIndex]; ok {
					continue
				}
				kept = append(kept, candidate)
			}
			return kept
		}
	}

	excluded := make(map[int]struct{}, len(composites))
	for _, composite := range composites {
		result.Decisions[composite.candidate.decisionIndex].Reason = "combined_play_all_extra"
		result.ExtraCount++
		excluded[composite.candidate.decisionIndex] = struct{}{}
	}
	kept := make([]tvTitleCandidate, 0, len(alive)-len(excluded))
	for _, candidate := range alive {
		if _, ok := excluded[candidate.decisionIndex]; ok {
			continue
		}
		kept = append(kept, candidate)
	}
	return kept
}

// detectComposites scans candidates longest-first for titles explainable as a
// combination of other non-composite candidates.
func detectComposites(alive []tvTitleCandidate) []compositeTitle {
	sorted := append([]tvTitleCandidate(nil), alive...)
	slices.SortFunc(sorted, func(a, b tvTitleCandidate) int {
		if a.title.Duration != b.title.Duration {
			return b.title.Duration - a.title.Duration
		}
		return a.title.ID - b.title.ID
	})

	compositeIndexes := make(map[int]struct{})
	composites := make([]compositeTitle, 0)
	for _, candidate := range sorted {
		others := make([]tvTitleCandidate, 0, len(sorted)-1)
		for _, other := range sorted {
			if other.decisionIndex == candidate.decisionIndex {
				continue
			}
			if _, ok := compositeIndexes[other.decisionIndex]; ok {
				continue
			}
			others = append(others, other)
		}
		components, ok := compositeComponents(others, candidate)
		if !ok {
			continue
		}
		composites = append(composites, compositeTitle{candidate: candidate, components: components})
		compositeIndexes[candidate.decisionIndex] = struct{}{}
	}
	return composites
}

// compositeComponents proves candidate is a combination of other candidates:
// either a pair whose segment union (or symmetric difference, for playlists
// that share a common segment) matches, or three-plus candidates whose
// segment sets partition the composite exactly. Durations must agree with the
// combination in both forms.
func compositeComponents(others []tvTitleCandidate, candidate tvTitleCandidate) ([]tvTitleCandidate, bool) {
	combinedSegs, ok := parseSegmentSet(candidate.title.SegmentMap)
	if !ok {
		return nil, false
	}

	for i := 0; i < len(others); i++ {
		for j := i + 1; j < len(others); j++ {
			a, b := others[i], others[j]
			segA, okA := parseSegmentSet(a.title.SegmentMap)
			segB, okB := parseSegmentSet(b.title.SegmentMap)
			if !okA || !okB {
				continue
			}
			if maps.Equal(segA, combinedSegs) || maps.Equal(segB, combinedSegs) {
				continue
			}
			if !segmentsLookCombined(segA, segB, combinedSegs) {
				continue
			}
			if !durationsLookCombined(a.title.Duration+b.title.Duration, candidate.title.Duration) {
				continue
			}
			return []tvTitleCandidate{a, b}, true
		}
	}

	subset := make([]tvTitleCandidate, 0, len(others))
	union := make(map[int]struct{}, len(combinedSegs))
	durationSum := 0
	for _, other := range others {
		segs, ok := parseSegmentSet(other.title.SegmentMap)
		if !ok || maps.Equal(segs, combinedSegs) {
			continue
		}
		contained := true
		for seg := range segs {
			if _, ok := combinedSegs[seg]; !ok {
				contained = false
				break
			}
		}
		if !contained {
			continue
		}
		subset = append(subset, other)
		maps.Copy(union, segs)
		durationSum += other.title.Duration
	}
	if len(subset) >= 3 && maps.Equal(union, combinedSegs) && durationsLookCombined(durationSum, candidate.title.Duration) {
		return subset, true
	}
	return nil, false
}

func hasSameLengthAlternate(alive []tvTitleCandidate, composite compositeTitle) bool {
	componentIndexes := make(map[int]struct{}, len(composite.components))
	for _, component := range composite.components {
		componentIndexes[component.decisionIndex] = struct{}{}
	}
	threshold := int(float64(composite.candidate.title.Duration) * tvSameLengthRatio)
	for _, candidate := range alive {
		if candidate.decisionIndex == composite.candidate.decisionIndex {
			continue
		}
		if _, ok := componentIndexes[candidate.decisionIndex]; ok {
			continue
		}
		if abs(candidate.title.Duration-composite.candidate.title.Duration) <= threshold {
			return true
		}
	}
	return false
}

// excludeRuntimeMismatches drops candidates whose duration matches neither a
// TMDB expected episode runtime nor an adjacent double-episode sum. The
// filter only fires when the expectations demonstrably describe this disc:
// at least one surviving candidate must match. Absent or incompatible
// expectations never exclude anything, so a disc of extended cuts (or a
// season with no TMDB runtimes) degrades to structural evidence only.
func excludeRuntimeMismatches(alive []tvTitleCandidate, expected []tmdb.Episode, result *tvTitleSelectionResult) []tvTitleCandidate {
	targets := expectedRuntimeTargets(expected)
	result.ExpectedRuntimeTargets = targets
	if len(targets) == 0 || len(alive) == 0 {
		return alive
	}

	matched := make([]tvTitleCandidate, 0, len(alive))
	mismatched := make([]tvTitleCandidate, 0)
	for _, candidate := range alive {
		if fitsExpectedRuntime(candidate.title.Duration, targets) {
			matched = append(matched, candidate)
		} else {
			mismatched = append(mismatched, candidate)
		}
	}
	if len(mismatched) == 0 {
		return alive
	}
	if len(matched) == 0 {
		result.Ambiguous = true
		result.AmbiguityReasons = append(result.AmbiguityReasons, "expected_runtimes_incompatible_with_disc")
		return alive
	}
	for _, candidate := range mismatched {
		result.Decisions[candidate.decisionIndex].Reason = "expected_runtime_mismatch"
		result.ExtraCount++
	}
	return matched
}

// expectedRuntimeTargets converts TMDB episode runtimes (minutes) to seconds
// and adds adjacent-pair sums so combined double-episode titles match.
func expectedRuntimeTargets(expected []tmdb.Episode) []int {
	targets := make([]int, 0, len(expected)*2)
	for i, episode := range expected {
		if episode.Runtime <= 0 {
			continue
		}
		targets = append(targets, episode.Runtime*60)
		if i+1 < len(expected) && expected[i+1].Runtime > 0 {
			targets = append(targets, (episode.Runtime+expected[i+1].Runtime)*60)
		}
	}
	return targets
}

func fitsExpectedRuntime(duration int, targets []int) bool {
	for _, target := range targets {
		tolerance := max(int(float64(target)*tvRuntimeToleranceRatio), tvRuntimeToleranceSec)
		if abs(duration-target) <= tolerance {
			return true
		}
	}
	return false
}

// capToExpectedCount bounds the rip plan by the TMDB season episode count,
// preferring the candidates whose durations best fit the expected runtimes.
func capToExpectedCount(alive []tvTitleCandidate, expected []tmdb.Episode, result *tvTitleSelectionResult) []tvTitleCandidate {
	if len(expected) == 0 || len(alive) <= len(expected) {
		return alive
	}
	targets := expectedRuntimeTargets(expected)
	ranked := append([]tvTitleCandidate(nil), alive...)
	slices.SortFunc(ranked, func(a, b tvTitleCandidate) int {
		fitA, fitB := runtimeFit(a.title.Duration, targets), runtimeFit(b.title.Duration, targets)
		if fitA != fitB {
			if fitA < fitB {
				return -1
			}
			return 1
		}
		return a.title.ID - b.title.ID
	})
	for _, candidate := range ranked[len(expected):] {
		result.Decisions[candidate.decisionIndex].Reason = "over_expected_episode_count"
		result.ExtraCount++
	}
	result.Ambiguous = true
	result.AmbiguityReasons = append(result.AmbiguityReasons, "candidates_exceed_expected_episodes")
	kept := ranked[:len(expected)]
	slices.SortFunc(kept, func(a, b tvTitleCandidate) int { return a.decisionIndex - b.decisionIndex })
	return kept
}

// runtimeFit returns the best relative deviation from any expected runtime
// target; 0 when no targets exist so ranking degrades to title order.
func runtimeFit(duration int, targets []int) float64 {
	if len(targets) == 0 {
		return 0
	}
	best := -1.0
	for _, target := range targets {
		if target <= 0 {
			continue
		}
		deviation := float64(abs(duration-target)) / float64(target)
		if best < 0 || deviation < best {
			best = deviation
		}
	}
	if best < 0 {
		return 0
	}
	return best
}

// orderSelectedTitles orders probable double-length titles first (contentid's
// opening-double inference reads episode order), then the rest by title ID.
func orderSelectedTitles(alive []tvTitleCandidate, decisions []tvTitleDecision) []ripspec.Title {
	doubles := make([]tvTitleCandidate, 0, 1)
	singles := make([]tvTitleCandidate, 0, len(alive))
	for _, candidate := range alive {
		if decisions[candidate.decisionIndex].Reason == "combined_double_episode_candidate" || looksDoubleLength(candidate, alive) {
			doubles = append(doubles, candidate)
		} else {
			singles = append(singles, candidate)
		}
	}
	byID := func(a, b tvTitleCandidate) int { return a.title.ID - b.title.ID }
	slices.SortFunc(doubles, byID)
	slices.SortFunc(singles, byID)

	titles := make([]ripspec.Title, 0, len(alive))
	for _, candidate := range append(doubles, singles...) {
		titles = append(titles, candidate.title)
	}
	return titles
}

func looksDoubleLength(candidate tvTitleCandidate, alive []tvTitleCandidate) bool {
	rest := make([]int, 0, len(alive)-1)
	for _, other := range alive {
		if other.decisionIndex == candidate.decisionIndex {
			continue
		}
		rest = append(rest, other.title.Duration)
	}
	if len(rest) < 2 {
		return false
	}
	slices.Sort(rest)
	median := rest[len(rest)/2]
	if median <= 0 {
		return false
	}
	duration := candidate.title.Duration
	return duration >= int(float64(median)*tvDoubleMinRatio) && duration <= int(float64(median)*tvDoubleMaxRatio)
}

func longestCandidate(candidates []tvTitleCandidate) tvTitleCandidate {
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.title.Duration > best.title.Duration ||
			(candidate.title.Duration == best.title.Duration && candidate.title.ID < best.title.ID) {
			best = candidate
		}
	}
	return best
}

func dedupKey(title ripspec.Title) string {
	key := strings.TrimSpace(title.SegmentMap)
	if key != "" {
		return key
	}
	return strings.TrimSpace(title.TitleHash)
}

func segmentsLookCombined(a, b, combined map[int]struct{}) bool {
	union := maps.Clone(a)
	maps.Copy(union, b)
	if maps.Equal(union, combined) {
		return true
	}

	// Some Blu-ray playlists share a small common segment on each individual
	// episode title. The hidden combined playlist may omit that shared segment
	// rather than representing the exact map union, while still being just a
	// play-all concat of the two episode titles. Treat the symmetric difference
	// as combined too so it can be excluded instead of selected as another
	// episode.
	symmetricDifference := make(map[int]struct{}, len(union))
	for segment := range a {
		if _, inB := b[segment]; !inB {
			symmetricDifference[segment] = struct{}{}
		}
	}
	for segment := range b {
		if _, inA := a[segment]; !inA {
			symmetricDifference[segment] = struct{}{}
		}
	}
	return len(symmetricDifference) > 0 && maps.Equal(symmetricDifference, combined)
}

func tvComponentsOverlap(components []tvTitleCandidate) bool {
	seen := make(map[int]struct{})
	for _, component := range components {
		segments, ok := parseSegmentSet(component.title.SegmentMap)
		if !ok {
			return false
		}
		for segment := range segments {
			if _, exists := seen[segment]; exists {
				return true
			}
			seen[segment] = struct{}{}
		}
	}
	return false
}

func parseSegmentSet(segmentMap string) (map[int]struct{}, bool) {
	segmentMap = strings.TrimSpace(segmentMap)
	if segmentMap == "" {
		return nil, false
	}
	parts := strings.Split(segmentMap, ",")
	result := make(map[int]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		result[value] = struct{}{}
	}
	return result, len(result) > 0
}

func durationsLookCombined(componentSum, combined int) bool {
	if componentSum <= 0 || combined <= 0 {
		return false
	}
	delta := abs(componentSum - combined)
	threshold := max(90, combined/20)
	return delta <= threshold
}

func summarizeAmbiguity(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	return strings.Join(reasons, ", ")
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
