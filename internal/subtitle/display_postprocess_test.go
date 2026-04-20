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
