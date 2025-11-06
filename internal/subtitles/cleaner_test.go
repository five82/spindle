package subtitles

import (
	"strings"
	"testing"
)

func TestCleanSRTRemovesAdvertisementCues(t *testing.T) {
	raw := `1
00:00:01,000 --> 00:00:03,000
www.OpenSubtitles.org

2
00:00:04,000 --> 00:00:06,000
Hello there!

3
00:00:07,000 --> 00:00:09,000
Subtitle by AwesomeSubs
`

	cleaned, stats := CleanSRT([]byte(raw))
	if stats.RemovedCues != 2 {
		t.Fatalf("expected 2 cues removed, got %d", stats.RemovedCues)
	}
	output := string(cleaned)
	if strings.Contains(strings.ToLower(output), "opensubtitles") {
		t.Fatalf("expected advertisement to be removed, got %q", output)
	}
	if strings.Contains(strings.ToLower(output), "subtitle by") {
		t.Fatalf("expected sponsor cue to be removed, got %q", output)
	}
	if !strings.Contains(output, "Hello there!") {
		t.Fatalf("expected dialogue to remain, got %q", output)
	}
	if !strings.HasSuffix(output, "\n") {
		t.Fatalf("expected trailing newline, got %q", output)
	}
}

func TestCleanSRTKeepsValidBlocks(t *testing.T) {
	raw := `1
00:00:01,000 --> 00:00:02,000
First line

2
00:00:03,000 --> 00:00:04,000
Second line
`
	cleaned, stats := CleanSRT([]byte(raw))
	if stats.RemovedCues != 0 {
		t.Fatalf("expected 0 cues removed, got %d", stats.RemovedCues)
	}
	output := string(cleaned)
	if strings.Count(output, "\n\n") != 1 {
		t.Fatalf("expected cues to remain separated by single blank line, got %q", output)
	}
}
