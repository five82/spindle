package subtitle

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/five82/spindle/internal/srtutil"
)

func TestWrapDisplayCueText_BalancesTwoLines(t *testing.T) {
	wrapped, changed := wrapDisplayCueText("This subtitle line is intentionally too long for a single line.")
	if !changed {
		t.Fatalf("expected cue text to be wrapped")
	}
	lines := strings.Split(wrapped, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), wrapped)
	}
	for _, line := range lines {
		if got := len([]rune(line)); got > maxSubtitleCharsPerLine {
			t.Fatalf("line length = %d, want <= %d: %q", got, maxSubtitleCharsPerLine, line)
		}
	}
}

func TestBestTwoLineBreak_Heuristics(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		wantA string
		wantB string
	}{
		{
			name:  "breaks after punctuation",
			text:  "I tried to warn you, but nobody wanted to listen",
			wantA: "I tried to warn you,",
			wantB: "but nobody wanted to listen",
		},
		{
			name:  "breaks before conjunction",
			text:  "We can stay right here and wait for the others",
			wantA: "We can stay right here",
			wantB: "and wait for the others",
		},
		{
			name:  "breaks before preposition",
			text:  "We hid the package safely under the old bridge",
			wantA: "We hid the package safely",
			wantB: "under the old bridge",
		},
		{
			name:  "avoids article noun split",
			text:  "This is the old bridge we talked about yesterday",
			wantA: "This is the old bridge",
			wantB: "we talked about yesterday",
		},
		{
			name:  "avoids pronoun verb split",
			text:  "I know what she said about the missing package",
			wantA: "I know what she said",
			wantB: "about the missing package",
		},
		{
			name:  "avoids auxiliary verb split",
			text:  "They said we should leave before anyone finds us",
			wantA: "They said we should leave",
			wantB: "before anyone finds us",
		},
		{
			name:  "avoids two word top line",
			text:  "All right, we should leave before anyone finds us tonight",
			wantA: "All right, we should leave",
			wantB: "before anyone finds us tonight",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			words := strings.Fields(tt.text)
			breakAt, ok := bestTwoLineBreak(words, maxSubtitleCharsPerLine)
			if !ok {
				t.Fatalf("bestTwoLineBreak() returned !ok")
			}
			gotA := strings.Join(words[:breakAt], " ")
			gotB := strings.Join(words[breakAt:], " ")
			if gotA != tt.wantA || gotB != tt.wantB {
				t.Fatalf("break = %q / %q, want %q / %q", gotA, gotB, tt.wantA, tt.wantB)
			}
		})
	}
}

func TestSplitDisplayCue_KeepsReadableCueIntact(t *testing.T) {
	cue := srtutil.Cue{
		Index: 1,
		Start: 0,
		End:   8.3,
		Text:  "We've been ordered to learn all we can about the Ferengi right now.",
	}
	parts := splitDisplayCue(cue)
	if len(parts) != 1 {
		t.Fatalf("expected readable cue to stay intact, got %d part(s)", len(parts))
	}
}

func TestSplitDisplayCue_BreaksOverfullCue(t *testing.T) {
	cue := srtutil.Cue{
		Index: 1,
		Start: 0,
		End:   12,
		Text:  "Our mission is to intercept and recover a T9 energy converter which the Ferengi stole from an unmanned monitor post on Gamma Tauri IV.",
	}
	parts := splitDisplayCue(cue)
	if len(parts) < 2 {
		t.Fatalf("expected overfull cue to be split, got %d part(s)", len(parts))
	}
	if parts[0].Start != cue.Start || parts[len(parts)-1].End != cue.End {
		t.Fatalf("split cue timing not preserved: first %.3f last %.3f", parts[0].Start, parts[len(parts)-1].End)
	}
	for _, part := range parts {
		if got := utf8.RuneCountInString(normalizeCueWhitespace(part.Text)); got > maxSubtitleCueChars {
			t.Fatalf("part too long: %d chars in %q", got, part.Text)
		}
	}
}

func TestSplitDisplayCue_ShortOverfullCuePreservesMonotonicTiming(t *testing.T) {
	cue := srtutil.Cue{
		Index: 1,
		Start: 10,
		End:   11,
		Text:  "This is a very long subtitle cue that has to be split even though the original timing is too short for every part to meet the preferred minimum duration.",
	}
	parts := splitDisplayCue(cue)
	if len(parts) < 2 {
		t.Fatalf("expected short overfull cue to split, got %d part(s)", len(parts))
	}
	if parts[0].Start != cue.Start || parts[len(parts)-1].End != cue.End {
		t.Fatalf("split cue timing not preserved: first %.3f last %.3f", parts[0].Start, parts[len(parts)-1].End)
	}
	for i, part := range parts {
		if part.End < part.Start {
			t.Fatalf("part %d has inverted timing: %.3f --> %.3f", i, part.Start, part.End)
		}
		if i > 0 && part.Start < parts[i-1].End {
			t.Fatalf("part %d overlaps previous: %.3f < %.3f", i, part.Start, parts[i-1].End)
		}
	}
}

func TestSplitWordsIntoCueChunks_CutsAtPreferredBoundary(t *testing.T) {
	words := strings.Fields("Our mission is to intercept and recover a T9 energy converter which the Ferengi stole from an unmanned monitor post on Gamma Tauri IV.")
	chunks := splitWordsIntoCueChunks(words)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if strings.Fields(chunk) == nil {
			t.Fatalf("chunk has no words: %q", chunk)
		}
		if !cueWrapsCleanly(chunk) {
			t.Fatalf("chunk does not wrap cleanly: %q", chunk)
		}
	}
	if strings.Join(chunks, " ") != strings.Join(words, " ") {
		t.Fatalf("chunks do not reconstruct original text: %q", strings.Join(chunks, " "))
	}
}

func TestMergeDisplayCues_ShortCuesMergeAcrossSmallGap(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 0.0, End: 0.4, Text: "Hi there"},
		{Index: 2, Start: 0.5, End: 1.0, Text: "friend"},
	}
	merged, count := mergeDisplayCues(cues)
	if count != 1 {
		t.Fatalf("MergedCues = %d, want 1", count)
	}
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged cue, got %d", len(merged))
	}
	if merged[0].Start != 0.0 || merged[0].End != 1.0 {
		t.Fatalf("merged span = %.3f --> %.3f, want 0.000 --> 1.000", merged[0].Start, merged[0].End)
	}
	if merged[0].Text != "Hi there friend" {
		t.Fatalf("merged text = %q, want %q", merged[0].Text, "Hi there friend")
	}
}

func TestMergeDisplayCues_ChainMergesIntoOne(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 0.0, End: 0.3, Text: "One"},
		{Index: 2, Start: 0.35, End: 0.65, Text: "two"},
		{Index: 3, Start: 0.7, End: 1.0, Text: "three"},
	}
	merged, count := mergeDisplayCues(cues)
	if len(merged) != 1 {
		t.Fatalf("expected chain to merge into 1 cue, got %d", len(merged))
	}
	if count != 2 {
		t.Fatalf("MergedCues = %d, want 2", count)
	}
	if merged[0].Text != "One two three" {
		t.Fatalf("merged text = %q, want %q", merged[0].Text, "One two three")
	}
	if merged[0].Start != 0.0 || merged[0].End != 1.0 {
		t.Fatalf("merged span = %.3f --> %.3f, want 0.000 --> 1.000", merged[0].Start, merged[0].End)
	}
}

func TestMergeDisplayCues_DoesNotMerge(t *testing.T) {
	t.Run("gap_too_large", func(t *testing.T) {
		cues := []srtutil.Cue{
			{Index: 1, Start: 0.0, End: 0.4, Text: "Short one"},
			{Index: 2, Start: 0.95, End: 1.4, Text: "Short two"},
		}
		merged, count := mergeDisplayCues(cues)
		if count != 0 || len(merged) != 2 {
			t.Fatalf("expected no merge across large gap, got count=%d len=%d", count, len(merged))
		}
	})

	t.Run("combined_span_too_long", func(t *testing.T) {
		cues := []srtutil.Cue{
			{Index: 1, Start: 0.0, End: 0.4, Text: "Short lead-in"},
			{Index: 2, Start: 0.45, End: 7.6, Text: "A long comfortable line of dialogue."},
		}
		merged, count := mergeDisplayCues(cues)
		if count != 0 || len(merged) != 2 {
			t.Fatalf("expected no merge when combined span exceeds max cue duration, got count=%d len=%d", count, len(merged))
		}
	})

	t.Run("combined_text_does_not_wrap_cleanly", func(t *testing.T) {
		cues := []srtutil.Cue{
			{Index: 1, Start: 0.0, End: 0.5, Text: "The weather today is unexpectedly quite nice"},
			{Index: 2, Start: 0.55, End: 3.5, Text: "and tomorrow it should remain sunny as well"},
		}
		merged, count := mergeDisplayCues(cues)
		if count != 0 || len(merged) != 2 {
			t.Fatalf("expected no merge when combined text can't wrap cleanly, got count=%d len=%d", count, len(merged))
		}
	})

	t.Run("both_cues_already_comfortable", func(t *testing.T) {
		cues := []srtutil.Cue{
			{Index: 1, Start: 0.0, End: 2.0, Text: "This is comfortable"},
			{Index: 2, Start: 2.1, End: 4.0, Text: "So is this one"},
		}
		merged, count := mergeDisplayCues(cues)
		if count != 0 || len(merged) != 2 {
			t.Fatalf("expected no merge when both cues already read comfortably, got count=%d len=%d", count, len(merged))
		}
	})
}

func TestMergeDisplayCues_HighReadingSpeedCueMergesAndImproves(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 0.0, End: 1.0, Text: "abcdefghijklmnopqrstuvwxyz1234"}, // 30 runes / 1.0s = 30 cps
		{Index: 2, Start: 1.05, End: 1.5, Text: "ok now"},
	}
	originalCPS := float64(utf8.RuneCountInString(cues[0].Text)) / (cues[0].End - cues[0].Start)
	merged, count := mergeDisplayCues(cues)
	if count != 1 || len(merged) != 1 {
		t.Fatalf("expected the pair to merge, got count=%d len=%d", count, len(merged))
	}
	mergedCPS := float64(utf8.RuneCountInString(normalizeCueWhitespace(merged[0].Text))) / (merged[0].End - merged[0].Start)
	if mergedCPS >= originalCPS {
		t.Fatalf("expected merged reading speed to drop below %.2f, got %.2f", originalCPS, mergedCPS)
	}
}

func TestRetimeDisplayCues_ExpandsShortCueIntoGapBudgets(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 0.0, End: 1.0, Text: "Intro"},
		{Index: 2, Start: 2.0, End: 2.8, Text: "You were going to show us something."},
		{Index: 3, Start: 4.4, End: 6.0, Text: "As requested, Captain."},
	}
	changed := retimeDisplayCues(cues, 10)
	if changed != 1 {
		t.Fatalf("retimeDisplayCues() changed %d cues, want 1", changed)
	}
	if !(cues[1].Start < 2.0 && cues[1].End > 2.8) {
		t.Fatalf("expected cue to expand around original timing, got %.3f --> %.3f", cues[1].Start, cues[1].End)
	}
	if cues[1].Start < cues[0].End {
		t.Fatalf("cue overlaps previous: %.3f < %.3f", cues[1].Start, cues[0].End)
	}
	if cues[2].Start < cues[1].End {
		t.Fatalf("cue overlaps next: %.3f < %.3f", cues[2].Start, cues[1].End)
	}
	cps := float64(len([]rune(normalizeCueWhitespace(cues[1].Text)))) / (cues[1].End - cues[1].Start)
	if cps > maxSubtitleReadingSpeed+0.01 {
		t.Fatalf("expected repaired cue cps <= %.1f, got %.2f", maxSubtitleReadingSpeed, cps)
	}
}

func TestRetimeDisplayCues_UsesWholeGapWhenNeighborDoesNotNeedIt(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 0.0, End: 1.0, Text: "Intro"},
		{Index: 2, Start: 4.0, End: 4.5, Text: "This cue needs the whole previous gap."},
	}
	changed := retimeDisplayCues(cues, 4.6)
	if changed != 1 {
		t.Fatalf("retimeDisplayCues() changed %d cues, want 1", changed)
	}
	if cues[1].Start < cues[0].End {
		t.Fatalf("cue overlaps previous: %.3f < %.3f", cues[1].Start, cues[0].End)
	}
	cps := float64(len([]rune(normalizeCueWhitespace(cues[1].Text)))) / (cues[1].End - cues[1].Start)
	if cps > maxSubtitleReadingSpeed+0.01 {
		t.Fatalf("expected repaired cue cps <= %.1f, got %.2f", maxSubtitleReadingSpeed, cps)
	}
}

func TestRetimeDisplayCues_HardCapsLongCue(t *testing.T) {
	cues := []srtutil.Cue{{
		Index: 1,
		Start: 10,
		End:   22,
		Text:  "Ordinary subtitle text that reads at a normal pace.",
	}}
	changed := retimeDisplayCues(cues, 60)
	if changed != 1 {
		t.Fatalf("retimeDisplayCues() changed %d cues, want 1", changed)
	}
	if cues[0].End != cues[0].Start+maxSubtitleCueDuration {
		t.Fatalf("expected hard cap to end at start+%.1f, got %.3f --> %.3f", maxSubtitleCueDuration, cues[0].Start, cues[0].End)
	}
}

func TestRetimeDisplayCues_UsesGapFreedByHardCap(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 0.0, End: 10.0, Text: "Waiting here for something to happen eventually."},
		{Index: 2, Start: 10.5, End: 11.0, Text: "This is much too fast to read."},
	}
	changed := retimeDisplayCues(cues, 20)
	if changed != 2 {
		t.Fatalf("retimeDisplayCues() changed %d cues, want 2", changed)
	}
	if cues[0].End != cues[0].Start+maxSubtitleCueDuration {
		t.Fatalf("expected first cue hard-capped to %.1fs, got end %.3f", maxSubtitleCueDuration, cues[0].End)
	}
	if !(cues[1].Start < 10.5 && cues[1].End > 11.0) {
		t.Fatalf("expected second cue to expand into freed gap, got %.3f --> %.3f", cues[1].Start, cues[1].End)
	}
	if cues[1].Start < cues[0].End {
		t.Fatalf("cue overlaps capped previous: %.3f < %.3f", cues[1].Start, cues[0].End)
	}
}

func TestRetimeDisplayCues_EnforcesMinimumGapOnOverlap(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 0.0, End: 6.0, Text: "Enough text to be comfortably paced already right now."},
		{Index: 2, Start: 5.5, End: 11.0, Text: "Another comfortable line of text right here indeed."},
	}
	retimeDisplayCues(cues, 20)
	if cues[0].End > cues[1].Start {
		t.Fatalf("cues still overlap: first ends %.3f, second starts %.3f", cues[0].End, cues[1].Start)
	}
	if gap := cues[1].Start - cues[0].End; gap < minSubtitleCueGap-1e-9 {
		t.Fatalf("gap between cues = %.4f, want >= %.4f", gap, minSubtitleCueGap)
	}
	if cues[0].End < cues[0].Start {
		t.Fatalf("first cue has inverted timing: %.3f --> %.3f", cues[0].Start, cues[0].End)
	}
}

func TestPostProcessDisplayCues_WrapsAndRetimes(t *testing.T) {
	cues := []srtutil.Cue{{
		Index: 1,
		Start: 1.0,
		End:   1.7,
		Text:  "This subtitle line is intentionally too long for a single line.",
	}}
	processed, stats := postProcessDisplayCues(cues, 10)
	if stats.SplitCues != 0 {
		t.Fatalf("SplitCues = %d, want 0", stats.SplitCues)
	}
	if stats.MergedCues != 0 {
		t.Fatalf("MergedCues = %d, want 0", stats.MergedCues)
	}
	if stats.WrappedCues != 1 {
		t.Fatalf("WrappedCues = %d, want 1", stats.WrappedCues)
	}
	if stats.RetimedCues != 1 {
		t.Fatalf("RetimedCues = %d, want 1", stats.RetimedCues)
	}
	if !strings.Contains(processed[0].Text, "\n") {
		t.Fatalf("expected wrapped cue text, got %q", processed[0].Text)
	}
	if processed[0].End-processed[0].Start <= 2.5 {
		t.Fatalf("expected cue duration to increase, got %.3f", processed[0].End-processed[0].Start)
	}
}

func TestPostProcessDisplayCues_EndToEndInvariants(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 0.0, End: 12.0, Text: "Our mission is to intercept and recover a T9 energy converter which the Ferengi stole from an unmanned monitor post on Gamma Tauri IV."},
		{Index: 2, Start: 12.1, End: 12.4, Text: "Hi"},
		{Index: 3, Start: 12.45, End: 12.6, Text: "there"},
		{Index: 4, Start: 20.0, End: 21.0, Text: "Normal dialogue line right here."},
		{Index: 5, Start: 21.05, End: 23.0, Text: "Something comfortably paced follows immediately."},
	}
	processed, _ := postProcessDisplayCues(cues, 30)
	if len(processed) == 0 {
		t.Fatalf("expected processed cues, got none")
	}
	for i, cue := range processed {
		lines := splitCueLines(cue.Text)
		if len(lines) > maxSubtitleLinesPerCue {
			t.Fatalf("cue %d has %d lines, want <= %d: %q", i, len(lines), maxSubtitleLinesPerCue, cue.Text)
		}
		if hasOverlongLine(lines) {
			t.Fatalf("cue %d has an overlong line: %q", i, cue.Text)
		}
		if duration := cue.End - cue.Start; duration > maxSubtitleCueDuration+1e-9 {
			t.Fatalf("cue %d duration = %.3f, want <= %.1f", i, duration, maxSubtitleCueDuration)
		}
		if i > 0 && cue.Start < processed[i-1].End {
			t.Fatalf("cue %d overlaps previous: %.3f < %.3f", i, cue.Start, processed[i-1].End)
		}
	}
}

func TestInsertMissingPunctuationSpaces(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Ow!which is so embarrassing.", "Ow! which is so embarrassing."},
		{"Shh, shh, shh, shh.Ah!", "Shh, shh, shh, shh. Ah!"},
		{"Really?I had no idea.", "Really? I had no idea."},
		{"First,second", "First, second"},
		{"He lives in the U.S.A. now.", "He lives in the U.S.A. now."},
		{"It costs 3.5 million, about 1,000 each.", "It costs 3.5 million, about 1,000 each."},
		{"See you at 9 a.m. Tomorrow works.", "See you at 9 a.m. Tomorrow works."},
		{"Don't stop believing.", "Don't stop believing."},
		{"Ow! Already spaced.", "Ow! Already spaced."},
	}
	for _, tc := range cases {
		if got := insertMissingPunctuationSpaces(tc.in); got != tc.want {
			t.Errorf("insertMissingPunctuationSpaces(%q) = %q, want %q", tc.in, got, tc.want)
		}
		// Repairs must be idempotent: normalize runs on every pass.
		if got := insertMissingPunctuationSpaces(tc.want); got != tc.want {
			t.Errorf("not idempotent: insertMissingPunctuationSpaces(%q) = %q", tc.want, got)
		}
	}
}

func TestPostProcessDisplayCues_RepairsGluedPunctuation(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 0.0, End: 2.5, Text: "Ow!which is so embarrassing."},
	}
	processed, _ := postProcessDisplayCues(cues, 100)
	if processed[0].Text != "Ow! which is so embarrassing." {
		t.Fatalf("glued punctuation not repaired: %q", processed[0].Text)
	}
}

func TestPostProcessDisplayCues_SecondPassIsIdempotent(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 0.0, End: 0.4, Text: "Hi there"},
		{Index: 2, Start: 0.5, End: 1.0, Text: "friend"},
		{Index: 3, Start: 4.0, End: 16.0, Text: "This is a deliberately long sentence that cannot possibly fit within a single wrapped subtitle cue, so it must be divided."},
		{Index: 4, Start: 20.0, End: 22.0, Text: "A comfortable\ntwo line cue that is wrapped already."},
	}
	first, _ := postProcessDisplayCues(cues, 100)
	second, stats := postProcessDisplayCues(first, 100)
	if stats.SplitCues != 0 || stats.MergedCues != 0 || stats.WrappedCues != 0 || stats.RetimedCues != 0 {
		t.Fatalf("second pass not a no-op: %+v", stats)
	}
	if len(second) != len(first) {
		t.Fatalf("second pass changed cue count: %d -> %d", len(first), len(second))
	}
	for i := range first {
		if second[i].Text != first[i].Text || second[i].Start != first[i].Start || second[i].End != first[i].End {
			t.Fatalf("cue %d changed on second pass: %+v -> %+v", i+1, first[i], second[i])
		}
	}
}
