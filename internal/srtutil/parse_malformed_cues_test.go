package srtutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSkipsMalformedBlocksWithoutLosingNextCue(t *testing.T) {
	content := "garbage\n1\ninvalid time\n2\n00:00:01,000 --> 00:00:03,000\nHello\nworld\n\n3\n"
	cues := Parse(content)
	if len(cues) != 1 || cues[0].Index != 2 || cues[0].Start != 1 || cues[0].Text != "Hello\nworld" {
		t.Fatalf("parsed cues: %+v", cues)
	}
	if got := PlainText(cues); got != "Hello world" {
		t.Fatalf("plain text: %q", got)
	}
	if got := Parse("4\n"); len(got) != 0 {
		t.Fatalf("truncated cue: %+v", got)
	}
	if _, err := ParseFile(filepath.Join(t.TempDir(), "missing.srt")); err == nil {
		t.Fatal("missing SRT accepted")
	}
	path := filepath.Join(t.TempDir(), "valid.srt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	fromFile, err := ParseFile(path)
	if err != nil || len(fromFile) != 1 {
		t.Fatalf("file parse: %+v %v", fromFile, err)
	}
}

func TestTimestampRejectsEveryMalformedField(t *testing.T) {
	for _, value := range []string{"", "xx:00:01,000", "00:xx:01,000", "00:00:xx,000", "00:00:01", "00:00:01,xxx"} {
		if got := ParseTimestamp(value); got != 0 {
			t.Errorf("invalid timestamp %q: %f", value, got)
		}
	}
}
