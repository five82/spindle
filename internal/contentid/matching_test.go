package contentid

import (
	"testing"
)

func TestSelectAnchorWindowFirstAnchor(t *testing.T) {
	t.Parallel()

	rips := []ripFingerprint{
		{EpisodeKey: "s02_001", Vector: newFingerprint("batman villain puzzler episode twenty five marker")},
		{EpisodeKey: "s02_002", Vector: newFingerprint("robin riddle episode twenty six marker")},
		{EpisodeKey: "s02_003", Vector: newFingerprint("alfred cave episode twenty seven marker")},
		{EpisodeKey: "s02_004", Vector: newFingerprint("gordon signal episode twenty eight marker")},
		{EpisodeKey: "s02_005", Vector: newFingerprint("catwoman chase episode twenty nine marker")},
		{EpisodeKey: "s02_006", Vector: newFingerprint("penguin umbrella episode thirty marker")},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 24, Vector: newFingerprint("different content episode twenty four")},
		{EpisodeNumber: 25, Vector: newFingerprint("batman villain puzzler episode twenty five marker")},
		{EpisodeNumber: 26, Vector: newFingerprint("robin riddle episode twenty six marker")},
		{EpisodeNumber: 30, Vector: newFingerprint("penguin umbrella episode thirty marker")},
	}

	anchor, ok := selectAnchorWindow(rips, refs, 60, DefaultPolicy().AnchorMinScore, DefaultPolicy().AnchorMinScoreMargin)
	if !ok {
		t.Fatalf("expected first anchor selection to succeed, got reason=%q", anchor.Reason)
	}
	if anchor.Reason != "first_anchor" {
		t.Fatalf("expected first_anchor reason, got %q", anchor.Reason)
	}
	if anchor.WindowStart != 25 || anchor.WindowEnd != 30 {
		t.Fatalf("expected window 25-30, got %d-%d", anchor.WindowStart, anchor.WindowEnd)
	}
}

func TestSelectAnchorWindowSecondAnchorFallback(t *testing.T) {
	t.Parallel()

	rips := []ripFingerprint{
		{EpisodeKey: "s02_001", Vector: newFingerprint("batman shared ambiguous anchor text")},
		{EpisodeKey: "s02_002", Vector: newFingerprint("robin exact episode twenty six marker")},
		{EpisodeKey: "s02_003", Vector: newFingerprint("episode twenty seven marker")},
		{EpisodeKey: "s02_004", Vector: newFingerprint("episode twenty eight marker")},
		{EpisodeKey: "s02_005", Vector: newFingerprint("episode twenty nine marker")},
		{EpisodeKey: "s02_006", Vector: newFingerprint("episode thirty marker")},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 25, Vector: newFingerprint("batman shared ambiguous anchor text")},
		{EpisodeNumber: 40, Vector: newFingerprint("batman shared ambiguous anchor text")},
		{EpisodeNumber: 26, Vector: newFingerprint("robin exact episode twenty six marker")},
		{EpisodeNumber: 27, Vector: newFingerprint("episode twenty seven marker")},
	}

	anchor, ok := selectAnchorWindow(rips, refs, 60, DefaultPolicy().AnchorMinScore, DefaultPolicy().AnchorMinScoreMargin)
	if !ok {
		t.Fatalf("expected second anchor selection to succeed, got reason=%q", anchor.Reason)
	}
	if anchor.Reason != "second_anchor" {
		t.Fatalf("expected second_anchor reason, got %q", anchor.Reason)
	}
	if anchor.WindowStart != 25 || anchor.WindowEnd != 30 {
		t.Fatalf("expected window 25-30, got %d-%d", anchor.WindowStart, anchor.WindowEnd)
	}
}

func TestSelectAnchorWindowFailsWhenBothAnchorsAmbiguous(t *testing.T) {
	t.Parallel()

	rips := []ripFingerprint{
		{EpisodeKey: "s02_001", Vector: newFingerprint("same anchor text")},
		{EpisodeKey: "s02_002", Vector: newFingerprint("same anchor text")},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 10, Vector: newFingerprint("same anchor text")},
		{EpisodeNumber: 20, Vector: newFingerprint("same anchor text")},
	}

	anchor, ok := selectAnchorWindow(rips, refs, 60, DefaultPolicy().AnchorMinScore, DefaultPolicy().AnchorMinScoreMargin)
	if ok {
		t.Fatalf("expected ambiguous anchors to fail, got window %d-%d", anchor.WindowStart, anchor.WindowEnd)
	}
	if anchor.Reason != "anchor_score_ambiguous" {
		t.Fatalf("expected anchor_score_ambiguous reason, got %q", anchor.Reason)
	}
}

func TestResolveEpisodeMatchesOptimalAssignment(t *testing.T) {
	t.Parallel()

	// Construct two episodes with cross-similarity where greedy would swap.
	rip1 := newFingerprint("alpha beta gamma intro theme park")              // intended E1
	rip2 := newFingerprint("delta epsilon zeta magic heroes rescue mission") // intended E2

	ref1 := referenceFingerprint{EpisodeNumber: 1, Vector: newFingerprint("alpha beta gamma park theme opening")}
	ref2 := referenceFingerprint{EpisodeNumber: 2, Vector: newFingerprint("delta epsilon zeta heroes rescue mission magic")}
	refCross := referenceFingerprint{EpisodeNumber: 3, Vector: newFingerprint("alpha beta gamma park magic heroes rescue mission")}

	rips := []ripFingerprint{{EpisodeKey: "s05e01", TitleID: 1, Vector: rip1}, {EpisodeKey: "s05e02", TitleID: 2, Vector: rip2}}
	refs := []referenceFingerprint{ref2, ref1, refCross}

	matches := resolveEpisodeMatches(rips, refs, DefaultPolicy().MinSimilarityScore)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	want := map[string]int{"s05e01": 1, "s05e02": 2}
	for _, m := range matches {
		if want[m.EpisodeKey] != m.TargetEpisode {
			t.Fatalf("unexpected match: %+v", m)
		}
	}
}

func TestResolveEpisodeMatchesThreshold(t *testing.T) {
	t.Parallel()

	rip := []ripFingerprint{{EpisodeKey: "s05e01", TitleID: 1, Vector: newFingerprint("barely similar")}}
	ref := []referenceFingerprint{{EpisodeNumber: 1, Vector: newFingerprint("completely different")}}
	matches := resolveEpisodeMatches(rip, ref, DefaultPolicy().MinSimilarityScore)
	if len(matches) != 0 {
		t.Fatalf("expected no matches below threshold, got %d", len(matches))
	}
}

func TestResolveEpisodeMatchesEmptyInputs(t *testing.T) {
	t.Parallel()

	t.Run("empty rips", func(t *testing.T) {
		refs := []referenceFingerprint{{EpisodeNumber: 1, Vector: newFingerprint("some content here")}}
		matches := resolveEpisodeMatches(nil, refs, DefaultPolicy().MinSimilarityScore)
		if matches != nil {
			t.Fatalf("expected nil for empty rips, got %v", matches)
		}
	})

	t.Run("empty refs", func(t *testing.T) {
		rips := []ripFingerprint{{EpisodeKey: "s01e01", TitleID: 1, Vector: newFingerprint("some content here")}}
		matches := resolveEpisodeMatches(rips, nil, DefaultPolicy().MinSimilarityScore)
		if matches != nil {
			t.Fatalf("expected nil for empty refs, got %v", matches)
		}
	})

	t.Run("both empty", func(t *testing.T) {
		matches := resolveEpisodeMatches(nil, nil, DefaultPolicy().MinSimilarityScore)
		if matches != nil {
			t.Fatalf("expected nil for both empty, got %v", matches)
		}
	})
}

func TestResolveEpisodeMatchesNilVectors(t *testing.T) {
	t.Parallel()

	t.Run("nil rip vector", func(t *testing.T) {
		rips := []ripFingerprint{{EpisodeKey: "s01e01", TitleID: 1, Vector: nil}}
		refs := []referenceFingerprint{{EpisodeNumber: 1, Vector: newFingerprint("episode one content dialog")}}
		matches := resolveEpisodeMatches(rips, refs, DefaultPolicy().MinSimilarityScore)
		if len(matches) != 0 {
			t.Fatalf("expected no matches with nil rip vector, got %d", len(matches))
		}
	})

	t.Run("nil ref vector", func(t *testing.T) {
		rips := []ripFingerprint{{EpisodeKey: "s01e01", TitleID: 1, Vector: newFingerprint("episode one content dialog")}}
		refs := []referenceFingerprint{{EpisodeNumber: 1, Vector: nil}}
		matches := resolveEpisodeMatches(rips, refs, DefaultPolicy().MinSimilarityScore)
		if len(matches) != 0 {
			t.Fatalf("expected no matches with nil ref vector, got %d", len(matches))
		}
	})
}

func TestResolveEpisodeMatchesPerfectMatch(t *testing.T) {
	t.Parallel()

	// Identical text should produce score of 1.0 (well above threshold)
	text := "the quick brown fox jumps over the lazy dog near the riverbank"
	rips := []ripFingerprint{{EpisodeKey: "s01e01", TitleID: 1, Vector: newFingerprint(text)}}
	refs := []referenceFingerprint{{EpisodeNumber: 1, Vector: newFingerprint(text), FileID: 42, Language: "en"}}

	matches := resolveEpisodeMatches(rips, refs, DefaultPolicy().MinSimilarityScore)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Score < 0.99 {
		t.Fatalf("expected near-perfect score for identical text, got %f", matches[0].Score)
	}
	if matches[0].SubtitleFileID != 42 {
		t.Fatalf("expected subtitle file ID 42, got %d", matches[0].SubtitleFileID)
	}
	if matches[0].SubtitleLanguage != "en" {
		t.Fatalf("expected language 'en', got %q", matches[0].SubtitleLanguage)
	}
}

func TestResolveEpisodeMatchesBoundaryThreshold(t *testing.T) {
	t.Parallel()

	// Test scores near the minSimilarityScore (0.58) boundary
	// Create content that should produce scores around the threshold

	t.Run("just above threshold", func(t *testing.T) {
		// Share enough vocabulary to be above 0.58
		rip := newFingerprint("hello world this test content shared vocabulary terms episode")
		ref := newFingerprint("hello world this test content shared vocabulary terms different")

		score := cosineSimilarity(rip, ref)
		if score < DefaultPolicy().MinSimilarityScore {
			t.Skipf("test fingerprints produce score %f below threshold %f, adjust test data", score, DefaultPolicy().MinSimilarityScore)
		}

		rips := []ripFingerprint{{EpisodeKey: "s01e01", TitleID: 1, Vector: rip}}
		refs := []referenceFingerprint{{EpisodeNumber: 1, Vector: ref}}
		matches := resolveEpisodeMatches(rips, refs, DefaultPolicy().MinSimilarityScore)
		if len(matches) != 1 {
			t.Fatalf("expected match above threshold (score=%f), got %d matches", score, len(matches))
		}
	})

	t.Run("just below threshold", func(t *testing.T) {
		// Limited shared vocabulary to be below 0.58
		rip := newFingerprint("alpha beta gamma delta epsilon zeta eta theta")
		ref := newFingerprint("one two three four five six seven eight")

		score := cosineSimilarity(rip, ref)
		if score >= DefaultPolicy().MinSimilarityScore {
			t.Skipf("test fingerprints produce score %f at or above threshold %f, adjust test data", score, DefaultPolicy().MinSimilarityScore)
		}

		rips := []ripFingerprint{{EpisodeKey: "s01e01", TitleID: 1, Vector: rip}}
		refs := []referenceFingerprint{{EpisodeNumber: 1, Vector: ref}}
		matches := resolveEpisodeMatches(rips, refs, DefaultPolicy().MinSimilarityScore)
		if len(matches) != 0 {
			t.Fatalf("expected no match below threshold (score=%f), got %d matches", score, len(matches))
		}
	})
}

func TestResolveEpisodeMatchesMoreRipsThanRefs(t *testing.T) {
	t.Parallel()

	// 4 rips, only 2 references available
	rips := []ripFingerprint{
		{EpisodeKey: "s01e01", TitleID: 1, Vector: newFingerprint("episode one dialog scene character speaking words")},
		{EpisodeKey: "s01e02", TitleID: 2, Vector: newFingerprint("episode two different scene another character talking")},
		{EpisodeKey: "s01e03", TitleID: 3, Vector: newFingerprint("episode three plot development story continues here")},
		{EpisodeKey: "s01e04", TitleID: 4, Vector: newFingerprint("episode four climax action sequence resolution ending")},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 1, Vector: newFingerprint("episode one dialog scene character speaking words")},
		{EpisodeNumber: 2, Vector: newFingerprint("episode two different scene another character talking")},
	}

	matches := resolveEpisodeMatches(rips, refs, DefaultPolicy().MinSimilarityScore)
	// Should match at most 2 (the number of refs)
	if len(matches) > 2 {
		t.Fatalf("expected at most 2 matches, got %d", len(matches))
	}
	// Verify the matches are correct
	for _, m := range matches {
		if m.EpisodeKey == "s01e01" && m.TargetEpisode != 1 {
			t.Fatalf("s01e01 should match episode 1, got %d", m.TargetEpisode)
		}
		if m.EpisodeKey == "s01e02" && m.TargetEpisode != 2 {
			t.Fatalf("s01e02 should match episode 2, got %d", m.TargetEpisode)
		}
	}
}

func TestResolveEpisodeMatchesMoreRefsThanRips(t *testing.T) {
	t.Parallel()

	// 2 rips, 4 references available - should still match correctly
	rips := []ripFingerprint{
		{EpisodeKey: "s01e03", TitleID: 3, Vector: newFingerprint("episode three plot development story continues here")},
		{EpisodeKey: "s01e04", TitleID: 4, Vector: newFingerprint("episode four climax action sequence resolution ending")},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 1, Vector: newFingerprint("episode one dialog scene character speaking words")},
		{EpisodeNumber: 2, Vector: newFingerprint("episode two different scene another character talking")},
		{EpisodeNumber: 3, Vector: newFingerprint("episode three plot development story continues here")},
		{EpisodeNumber: 4, Vector: newFingerprint("episode four climax action sequence resolution ending")},
	}

	matches := resolveEpisodeMatches(rips, refs, DefaultPolicy().MinSimilarityScore)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}

	matchMap := make(map[string]int)
	for _, m := range matches {
		matchMap[m.EpisodeKey] = m.TargetEpisode
	}
	if matchMap["s01e03"] != 3 {
		t.Fatalf("s01e03 should match episode 3, got %d", matchMap["s01e03"])
	}
	if matchMap["s01e04"] != 4 {
		t.Fatalf("s01e04 should match episode 4, got %d", matchMap["s01e04"])
	}
}

func TestResolveEpisodeMatchesFullSeason(t *testing.T) {
	t.Parallel()

	// Simulate a full 10-episode season
	var rips []ripFingerprint
	var refs []referenceFingerprint

	baseDialogs := []string{
		"pilot episode introduces main character backstory origin",
		"second episode develops plot introduces antagonist conflict",
		"third episode character growth challenges obstacles faced",
		"fourth episode midpoint revelation secrets revealed truth",
		"fifth episode consequences actions impact relationships change",
		"sixth episode recovery rebuilding trust allies gather",
		"seventh episode preparation final confrontation plans made",
		"eighth episode betrayal unexpected twist allegiances shift",
		"ninth episode climax battle showdown ultimate challenge",
		"tenth episode resolution conclusion ending new beginning",
	}

	for i := range 10 {
		var key string
		if i < 9 {
			key = "s01e0" + string(rune('1'+i))
		} else {
			key = "s01e10"
		}
		rips = append(rips, ripFingerprint{
			EpisodeKey: key,
			TitleID:    i + 1,
			Vector:     newFingerprint(baseDialogs[i]),
		})
		refs = append(refs, referenceFingerprint{
			EpisodeNumber: i + 1,
			Vector:        newFingerprint(baseDialogs[i]),
			FileID:        int64(100 + i),
			Language:      "en",
		})
	}

	matches := resolveEpisodeMatches(rips, refs, DefaultPolicy().MinSimilarityScore)
	if len(matches) != 10 {
		t.Fatalf("expected 10 matches for full season, got %d", len(matches))
	}

	// Verify all matches are correct
	for _, m := range matches {
		expectedEp := m.TitleID // TitleID matches episode number in this test
		if m.TargetEpisode != expectedEp {
			t.Errorf("episode key %s (title %d) matched to episode %d, expected %d",
				m.EpisodeKey, m.TitleID, m.TargetEpisode, expectedEp)
		}
	}
}

func TestResolveEpisodeMatchesSimilarScoresCorrectAssignment(t *testing.T) {
	t.Parallel()

	// Create scenarios where multiple rips have similar scores to multiple refs
	// The Hungarian algorithm should find the optimal assignment

	// All episodes share common vocabulary but have distinct phrases
	common := "the character walks into room and says hello goodbye "
	rips := []ripFingerprint{
		{EpisodeKey: "s01e01", TitleID: 1, Vector: newFingerprint(common + "alpha alpha alpha unique")},
		{EpisodeKey: "s01e02", TitleID: 2, Vector: newFingerprint(common + "beta beta beta special")},
		{EpisodeKey: "s01e03", TitleID: 3, Vector: newFingerprint(common + "gamma gamma gamma distinct")},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 1, Vector: newFingerprint(common + "alpha alpha alpha unique")},
		{EpisodeNumber: 2, Vector: newFingerprint(common + "beta beta beta special")},
		{EpisodeNumber: 3, Vector: newFingerprint(common + "gamma gamma gamma distinct")},
	}

	matches := resolveEpisodeMatches(rips, refs, DefaultPolicy().MinSimilarityScore)
	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(matches))
	}

	for _, m := range matches {
		expected := m.TitleID
		if m.TargetEpisode != expected {
			t.Errorf("%s matched to episode %d, expected %d (score=%f)",
				m.EpisodeKey, m.TargetEpisode, expected, m.Score)
		}
	}
}

func TestResolveEpisodeMatchesPreservesMetadata(t *testing.T) {
	t.Parallel()

	// Verify that all metadata fields are properly carried through
	rips := []ripFingerprint{
		{EpisodeKey: "s02e05", TitleID: 7, Vector: newFingerprint("specific episode content dialog speech")},
	}
	refs := []referenceFingerprint{
		{
			EpisodeNumber: 5,
			Vector:        newFingerprint("specific episode content dialog speech"),
			FileID:        12345,
			Language:      "es",
			CachePath:     "/cache/subtitles/show/s02e05.srt",
		},
	}

	matches := resolveEpisodeMatches(rips, refs, DefaultPolicy().MinSimilarityScore)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}

	m := matches[0]
	if m.EpisodeKey != "s02e05" {
		t.Errorf("expected EpisodeKey 's02e05', got %q", m.EpisodeKey)
	}
	if m.TitleID != 7 {
		t.Errorf("expected TitleID 7, got %d", m.TitleID)
	}
	if m.TargetEpisode != 5 {
		t.Errorf("expected TargetEpisode 5, got %d", m.TargetEpisode)
	}
	if m.SubtitleFileID != 12345 {
		t.Errorf("expected SubtitleFileID 12345, got %d", m.SubtitleFileID)
	}
	if m.SubtitleLanguage != "es" {
		t.Errorf("expected SubtitleLanguage 'es', got %q", m.SubtitleLanguage)
	}
	if m.SubtitleCachePath != "/cache/subtitles/show/s02e05.srt" {
		t.Errorf("expected SubtitleCachePath, got %q", m.SubtitleCachePath)
	}
}

func TestCosineSimilarityEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("nil inputs", func(t *testing.T) {
		if score := cosineSimilarity(nil, nil); score != 0 {
			t.Errorf("expected 0 for nil inputs, got %f", score)
		}
		if score := cosineSimilarity(newFingerprint("hello world"), nil); score != 0 {
			t.Errorf("expected 0 for nil second input, got %f", score)
		}
		if score := cosineSimilarity(nil, newFingerprint("hello world")); score != 0 {
			t.Errorf("expected 0 for nil first input, got %f", score)
		}
	})

	t.Run("empty fingerprint", func(t *testing.T) {
		// Short tokens (< 3 chars) are filtered out
		empty := newFingerprint("a b c")
		if empty != nil {
			t.Errorf("expected nil fingerprint for short tokens")
		}
	})

	t.Run("no overlap", func(t *testing.T) {
		a := newFingerprint("alpha beta gamma delta epsilon")
		b := newFingerprint("one two three four five six")
		score := cosineSimilarity(a, b)
		if score != 0 {
			t.Errorf("expected 0 for no token overlap, got %f", score)
		}
	})

	t.Run("identical content", func(t *testing.T) {
		text := "identical content repeated several times"
		a := newFingerprint(text)
		b := newFingerprint(text)
		score := cosineSimilarity(a, b)
		if score < 0.999 {
			t.Errorf("expected ~1.0 for identical content, got %f", score)
		}
	})
}

func TestRefineMatchBlockAllInBlock(t *testing.T) {
	t.Parallel()
	// All matches are already within a contiguous block — no changes.
	matches := []matchResult{
		{EpisodeKey: "s01e01", TitleID: 1, TargetEpisode: 1, Score: 0.95},
		{EpisodeKey: "s01e02", TitleID: 2, TargetEpisode: 2, Score: 0.93},
		{EpisodeKey: "s01e03", TitleID: 3, TargetEpisode: 3, Score: 0.91},
	}
	rips := []ripFingerprint{
		{EpisodeKey: "s01e01", TitleID: 1},
		{EpisodeKey: "s01e02", TitleID: 2},
		{EpisodeKey: "s01e03", TitleID: 3},
	}
	result, info := refineMatchBlock(matches, nil, rips, 10, 0, DefaultPolicy())
	if len(result) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(result))
	}
	if info.Displaced != 0 {
		t.Fatalf("expected 0 displaced, got %d", info.Displaced)
	}
}

func TestRefineMatchBlockOutliersReassigned(t *testing.T) {
	t.Parallel()
	// 4 matches: 2 high-confidence in block (E1, E2), 2 outliers (E22, E30).
	// Block should be E1-E4 (4 rips). Outliers reassigned to E3, E4.
	matches := []matchResult{
		{EpisodeKey: "s01e01", TitleID: 1, TargetEpisode: 1, Score: 0.97},
		{EpisodeKey: "s01e02", TitleID: 2, TargetEpisode: 2, Score: 0.95},
		{EpisodeKey: "s01e03", TitleID: 3, TargetEpisode: 22, Score: 0.88},
		{EpisodeKey: "s01e04", TitleID: 4, TargetEpisode: 30, Score: 0.87},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 1, Vector: newFingerprint("episode one content alpha beta")},
		{EpisodeNumber: 2, Vector: newFingerprint("episode two content gamma delta")},
		{EpisodeNumber: 3, Vector: newFingerprint("episode three content epsilon zeta")},
		{EpisodeNumber: 4, Vector: newFingerprint("episode four content theta iota")},
		{EpisodeNumber: 22, Vector: newFingerprint("episode twentytwo far away content")},
		{EpisodeNumber: 30, Vector: newFingerprint("episode thirty far away content")},
	}
	rips := []ripFingerprint{
		{EpisodeKey: "s01e01", TitleID: 1, Vector: newFingerprint("episode one content alpha beta")},
		{EpisodeKey: "s01e02", TitleID: 2, Vector: newFingerprint("episode two content gamma delta")},
		{EpisodeKey: "s01e03", TitleID: 3, Vector: newFingerprint("episode three content epsilon zeta")},
		{EpisodeKey: "s01e04", TitleID: 4, Vector: newFingerprint("episode four content theta iota")},
	}
	result, info := refineMatchBlock(matches, refs, rips, 34, 0, DefaultPolicy())
	if info.Displaced != 2 {
		t.Fatalf("expected 2 displaced, got %d", info.Displaced)
	}
	if info.Reassigned != 2 {
		t.Fatalf("expected 2 reassigned, got %d", info.Reassigned)
	}
	// Verify reassigned matches target E3 and E4
	targetSet := make(map[int]bool)
	for _, m := range result {
		targetSet[m.TargetEpisode] = true
	}
	if !targetSet[3] {
		t.Error("expected E3 in result after reassignment")
	}
	if !targetSet[4] {
		t.Error("expected E4 in result after reassignment")
	}
	// Outliers should no longer point to E22/E30
	if targetSet[22] {
		t.Error("E22 should have been displaced")
	}
	if targetSet[30] {
		t.Error("E30 should have been displaced")
	}
}

func TestRefineMatchBlockMismatchFlagsReview(t *testing.T) {
	t.Parallel()
	// 5 rips mapping to E4, E5, E15, E16, E17. Season only has 6 episodes.
	// Block E4-E8 → clamped to E4-E6. Valid = E4, E5. Displaced = 3 (E15,E16,E17).
	// Gaps = 1 (E6). 3 != 1 → needs review.
	matches := []matchResult{
		{EpisodeKey: "s01e01", TitleID: 1, TargetEpisode: 4, Score: 0.97},
		{EpisodeKey: "s01e02", TitleID: 2, TargetEpisode: 5, Score: 0.96},
		{EpisodeKey: "s01e03", TitleID: 3, TargetEpisode: 15, Score: 0.88},
		{EpisodeKey: "s01e04", TitleID: 4, TargetEpisode: 16, Score: 0.87},
		{EpisodeKey: "s01e05", TitleID: 5, TargetEpisode: 17, Score: 0.86},
	}
	rips := []ripFingerprint{
		{EpisodeKey: "s01e01", TitleID: 1},
		{EpisodeKey: "s01e02", TitleID: 2},
		{EpisodeKey: "s01e03", TitleID: 3},
		{EpisodeKey: "s01e04", TitleID: 4},
		{EpisodeKey: "s01e05", TitleID: 5},
	}
	_, info := refineMatchBlock(matches, nil, rips, 6, 0, DefaultPolicy())
	if !info.NeedsReview {
		t.Errorf("expected needs_review when displaced != gaps (displaced=%d, gaps=%d)", info.Displaced, info.Gaps)
	}
}

func TestRefineMatchBlockNoHighConfidence(t *testing.T) {
	t.Parallel()
	// Only 1 match — can't establish a block.
	matches := []matchResult{
		{EpisodeKey: "s01e01", TitleID: 1, TargetEpisode: 5, Score: 0.90},
	}
	rips := []ripFingerprint{
		{EpisodeKey: "s01e01", TitleID: 1},
	}
	result, info := refineMatchBlock(matches, nil, rips, 10, 0, DefaultPolicy())
	if len(result) != 1 {
		t.Fatalf("expected 1 match unchanged, got %d", len(result))
	}
	if info.Displaced != 0 {
		t.Fatalf("expected 0 displaced for single match, got %d", info.Displaced)
	}
}

func TestRefineMatchBlockDisc1ForcesStartAt1(t *testing.T) {
	t.Parallel()
	// Batman disc 1: 12 rips, Hungarian matched E02-E12 as high-conf, E01 as low.
	// Disc 1 should force blockStart=1, keeping E01 inside the block.
	matches := []matchResult{
		{EpisodeKey: "s01_012", TitleID: 12, TargetEpisode: 1, Score: 0.62},
		{EpisodeKey: "s01_001", TitleID: 1, TargetEpisode: 2, Score: 0.95},
		{EpisodeKey: "s01_002", TitleID: 2, TargetEpisode: 3, Score: 0.94},
		{EpisodeKey: "s01_003", TitleID: 3, TargetEpisode: 4, Score: 0.93},
		{EpisodeKey: "s01_004", TitleID: 4, TargetEpisode: 5, Score: 0.92},
		{EpisodeKey: "s01_005", TitleID: 5, TargetEpisode: 6, Score: 0.91},
		{EpisodeKey: "s01_006", TitleID: 6, TargetEpisode: 7, Score: 0.90},
		{EpisodeKey: "s01_007", TitleID: 7, TargetEpisode: 8, Score: 0.89},
		{EpisodeKey: "s01_008", TitleID: 8, TargetEpisode: 9, Score: 0.93},
		{EpisodeKey: "s01_009", TitleID: 9, TargetEpisode: 10, Score: 0.92},
		{EpisodeKey: "s01_010", TitleID: 10, TargetEpisode: 11, Score: 0.91},
		{EpisodeKey: "s01_011", TitleID: 11, TargetEpisode: 12, Score: 0.90},
	}
	rips := make([]ripFingerprint, 12)
	for i := range rips {
		rips[i] = ripFingerprint{EpisodeKey: matches[i].EpisodeKey, TitleID: matches[i].TitleID}
	}
	result, info := refineMatchBlock(matches, nil, rips, 24, 1, DefaultPolicy())
	if info.BlockStart != 1 {
		t.Fatalf("expected blockStart=1 for disc 1, got %d", info.BlockStart)
	}
	if info.BlockEnd != 12 {
		t.Fatalf("expected blockEnd=12, got %d", info.BlockEnd)
	}
	// E01 should remain in the block — no displacement.
	targetSet := make(map[int]bool)
	for _, m := range result {
		targetSet[m.TargetEpisode] = true
	}
	if !targetSet[1] {
		t.Error("E01 should be in the result (not displaced)")
	}
	if info.Displaced != 0 {
		t.Fatalf("expected 0 displaced for disc 1 anchored block, got %d", info.Displaced)
	}
}

func TestRefineMatchBlockDisc2ExpandsDownward(t *testing.T) {
	t.Parallel()
	// Disc 2 with 12 rips, actual E13-E24. High-conf E14-E24 (11 matches).
	// One displaced match originally pointed to E12 (below hcMin=14) → expand downward.
	matches := []matchResult{
		{EpisodeKey: "s01_012", TitleID: 12, TargetEpisode: 12, Score: 0.62}, // displaced, below hcMin
		{EpisodeKey: "s01_001", TitleID: 1, TargetEpisode: 14, Score: 0.95},
		{EpisodeKey: "s01_002", TitleID: 2, TargetEpisode: 15, Score: 0.94},
		{EpisodeKey: "s01_003", TitleID: 3, TargetEpisode: 16, Score: 0.93},
		{EpisodeKey: "s01_004", TitleID: 4, TargetEpisode: 17, Score: 0.92},
		{EpisodeKey: "s01_005", TitleID: 5, TargetEpisode: 18, Score: 0.91},
		{EpisodeKey: "s01_006", TitleID: 6, TargetEpisode: 19, Score: 0.90},
		{EpisodeKey: "s01_007", TitleID: 7, TargetEpisode: 20, Score: 0.89},
		{EpisodeKey: "s01_008", TitleID: 8, TargetEpisode: 21, Score: 0.93},
		{EpisodeKey: "s01_009", TitleID: 9, TargetEpisode: 22, Score: 0.92},
		{EpisodeKey: "s01_010", TitleID: 10, TargetEpisode: 23, Score: 0.91},
		{EpisodeKey: "s01_011", TitleID: 11, TargetEpisode: 24, Score: 0.90},
	}
	rips := make([]ripFingerprint, 12)
	for i := range rips {
		rips[i] = ripFingerprint{EpisodeKey: matches[i].EpisodeKey, TitleID: matches[i].TitleID}
	}
	_, info := refineMatchBlock(matches, nil, rips, 48, 2, DefaultPolicy())
	if info.BlockStart != 13 {
		t.Fatalf("expected blockStart=13 (expand downward for disc 2), got %d", info.BlockStart)
	}
	if info.BlockEnd != 24 {
		t.Fatalf("expected blockEnd=24, got %d", info.BlockEnd)
	}
}

func TestRefineMatchBlockDisc2ExpandsUpward(t *testing.T) {
	t.Parallel()
	// Disc 2 with 4 rips, actual E5-E8. High-conf E5-E7 (3 matches).
	// One displaced match originally pointed to E10 (above hcMax=7) → expand upward.
	matches := []matchResult{
		{EpisodeKey: "s01e01", TitleID: 1, TargetEpisode: 5, Score: 0.95},
		{EpisodeKey: "s01e02", TitleID: 2, TargetEpisode: 6, Score: 0.94},
		{EpisodeKey: "s01e03", TitleID: 3, TargetEpisode: 7, Score: 0.93},
		{EpisodeKey: "s01e04", TitleID: 4, TargetEpisode: 10, Score: 0.62}, // displaced, above hcMax
	}
	rips := make([]ripFingerprint, 4)
	for i := range rips {
		rips[i] = ripFingerprint{EpisodeKey: matches[i].EpisodeKey, TitleID: matches[i].TitleID}
	}
	_, info := refineMatchBlock(matches, nil, rips, 20, 2, DefaultPolicy())
	// validLow = 7-4+1 = 4, validHigh = 5. Displaced points above hcMax → blockStart = validHigh = 5.
	if info.BlockStart != 5 {
		t.Fatalf("expected blockStart=5 (expand upward for disc 2), got %d", info.BlockStart)
	}
	if info.BlockEnd != 8 {
		t.Fatalf("expected blockEnd=8, got %d", info.BlockEnd)
	}
}

func TestRefineMatchBlockDisc2PreventsStartAt1(t *testing.T) {
	t.Parallel()
	// Disc 2 with 4 rips, high-conf at E2-E4. Valid range [1,2].
	// Even though validLow=1, disc 2+ forces blockStart >= 2.
	matches := []matchResult{
		{EpisodeKey: "s01e01", TitleID: 1, TargetEpisode: 2, Score: 0.95},
		{EpisodeKey: "s01e02", TitleID: 2, TargetEpisode: 3, Score: 0.94},
		{EpisodeKey: "s01e03", TitleID: 3, TargetEpisode: 4, Score: 0.93},
		{EpisodeKey: "s01e04", TitleID: 4, TargetEpisode: 1, Score: 0.62}, // displaced, below hcMin
	}
	rips := make([]ripFingerprint, 4)
	for i := range rips {
		rips[i] = ripFingerprint{EpisodeKey: matches[i].EpisodeKey, TitleID: matches[i].TitleID}
	}
	_, info := refineMatchBlock(matches, nil, rips, 20, 2, DefaultPolicy())
	// validLow = max(1, 4-4+1) = 1. But disc 2 clamps to 2.
	if info.BlockStart < 2 {
		t.Fatalf("disc 2+ must not start at episode 1, got blockStart=%d", info.BlockStart)
	}
}

func TestRefineMatchBlockDiscUnknownPreservesUpwardExpansion(t *testing.T) {
	t.Parallel()
	// discNumber=0: should anchor at hcMin and expand upward (existing behavior).
	matches := []matchResult{
		{EpisodeKey: "s01e01", TitleID: 1, TargetEpisode: 5, Score: 0.95},
		{EpisodeKey: "s01e02", TitleID: 2, TargetEpisode: 6, Score: 0.94},
		{EpisodeKey: "s01e03", TitleID: 3, TargetEpisode: 7, Score: 0.93},
		{EpisodeKey: "s01e04", TitleID: 4, TargetEpisode: 20, Score: 0.62}, // outlier
	}
	rips := make([]ripFingerprint, 4)
	for i := range rips {
		rips[i] = ripFingerprint{EpisodeKey: matches[i].EpisodeKey, TitleID: matches[i].TitleID}
	}
	_, info := refineMatchBlock(matches, nil, rips, 30, 0, DefaultPolicy())
	// discNumber=0 uses hcMin=5 as blockStart, blockEnd=8.
	if info.BlockStart != 5 {
		t.Fatalf("expected blockStart=5 for disc unknown (anchor at hcMin), got %d", info.BlockStart)
	}
	if info.BlockEnd != 8 {
		t.Fatalf("expected blockEnd=8, got %d", info.BlockEnd)
	}
}

func TestRefineMatchBlockSingleEpisodeSkipped(t *testing.T) {
	t.Parallel()
	// Single match (movie-like) — should skip refinement.
	matches := []matchResult{
		{EpisodeKey: "s01e01", TitleID: 1, TargetEpisode: 1, Score: 0.99},
	}
	result, _ := refineMatchBlock(matches, nil, nil, 1, 0, DefaultPolicy())
	if len(result) != 1 || result[0].TargetEpisode != 1 {
		t.Fatalf("expected single match unchanged")
	}
}

func TestHungarianAlgorithmEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("single element", func(t *testing.T) {
		cost := [][]float64{{0.5}}
		assign := hungarian(cost)
		if len(assign) != 1 || assign[0] != 0 {
			t.Errorf("expected [0] for single element, got %v", assign)
		}
	})

	t.Run("empty matrix", func(t *testing.T) {
		assign := hungarian(nil)
		if assign != nil {
			t.Errorf("expected nil for empty matrix, got %v", assign)
		}
	})

	t.Run("non-square returns nil", func(t *testing.T) {
		// hungarian expects square matrix
		cost := [][]float64{{1, 2, 3}}
		assign := hungarian(cost)
		if assign != nil {
			t.Errorf("expected nil for non-square matrix, got %v", assign)
		}
	})
}
