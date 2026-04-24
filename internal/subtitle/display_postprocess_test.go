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
		if got := utf8.RuneCountInString(normalizeCueWhitespace(part.Text)); got > preferredDisplayMaxCueChars {
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
	if cps > preferredSubtitleReadingSpeed+0.01 {
		t.Fatalf("expected repaired cue cps <= %.1f, got %.2f", preferredSubtitleReadingSpeed, cps)
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
