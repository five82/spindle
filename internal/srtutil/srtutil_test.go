package srtutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"00:00:00,000", 0},
		{"00:00:01,500", 1.5},
		{"01:02:03,456", 3723.456},
		{"10:00:00,000", 36000},
		{"  00:00:01,000  ", 1.0}, // trim whitespace
		{"bad", 0},
		{"00:00", 0},
		{"00:00:00", 0},
		{"aa:bb:cc,ddd", 0},
	}
	for _, tt := range tests {
		got := ParseTimestamp(tt.input)
		if got != tt.want {
			t.Errorf("ParseTimestamp(%q) = %f, want %f", tt.input, got, tt.want)
		}
	}
}

func TestFormatTimestamp(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0, "00:00:00,000"},
		{1.5, "00:00:01,500"},
		{3723.456, "01:02:03,456"},
		{36000, "10:00:00,000"},
		{-5, "00:00:00,000"}, // clamp
	}
	for _, tt := range tests {
		got := FormatTimestamp(tt.input)
		if got != tt.want {
			t.Errorf("FormatTimestamp(%f) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseAndFormatRoundtrip(t *testing.T) {
	content := "1\n00:00:01,000 --> 00:00:02,500\nHello world\n\n" +
		"2\n00:00:03,000 --> 00:00:04,000\nLine one\nLine two\n"
	cues := Parse(content)
	if len(cues) != 2 {
		t.Fatalf("expected 2 cues, got %d", len(cues))
	}
	if cues[0].Index != 1 || cues[0].Start != 1.0 || cues[0].End != 2.5 || cues[0].Text != "Hello world" {
		t.Errorf("cue[0] = %+v", cues[0])
	}
	if cues[1].Text != "Line one\nLine two" {
		t.Errorf("cue[1].Text = %q", cues[1].Text)
	}

	reformatted := Format(cues)
	reparsed := Parse(reformatted)
	if len(reparsed) != len(cues) {
		t.Fatalf("roundtrip cue count changed: %d -> %d", len(cues), len(reparsed))
	}
	for i := range cues {
		if cues[i] != reparsed[i] {
			t.Errorf("roundtrip mismatch at %d: %+v != %+v", i, cues[i], reparsed[i])
		}
	}
}

func TestParseSkipsMalformedBlocks(t *testing.T) {
	content := "not a number\n00:00:00,000 --> 00:00:01,000\nskipped\n\n" +
		"1\n00:00:02,000 --> 00:00:03,000\nkept\n"
	cues := Parse(content)
	if len(cues) != 1 || cues[0].Text != "kept" {
		t.Fatalf("unexpected parse result: %+v", cues)
	}
}

func TestParseCRLF(t *testing.T) {
	content := "1\r\n00:00:01,000 --> 00:00:02,000\r\nhi\r\n"
	cues := Parse(content)
	if len(cues) != 1 || cues[0].Text != "hi" {
		t.Fatalf("CRLF parse failed: %+v", cues)
	}
}

func TestPlainText(t *testing.T) {
	cues := []Cue{
		{Text: "Hello"},
		{Text: "Line one\nLine two"},
		{Text: "   "}, // blank; skipped
		{Text: "Bye"},
	}
	got := PlainText(cues)
	want := "Hello Line one Line two Bye"
	if got != want {
		t.Errorf("PlainText = %q, want %q", got, want)
	}
}

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.srt")
	content := "1\n00:00:01,000 --> 00:00:02,000\nhi\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cues, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(cues) != 1 || cues[0].Text != "hi" {
		t.Fatalf("unexpected: %+v", cues)
	}
}

