package subtitles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSRT = `1
00:01:00,000 --> 00:01:05,000
Hello there.

2
00:10:00,000 --> 00:10:05,000
Middle dialogue line one.

3
00:15:00,000 --> 00:15:05,000
Middle dialogue line two.

4
00:20:00,000 --> 00:20:05,000
Near the end.

5
00:29:00,000 --> 00:29:05,000
Final line of dialogue.
`

func writeTempSRT(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.srt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMiddleSRTRange(t *testing.T) {
	t.Run("normal file", func(t *testing.T) {
		path := writeTempSRT(t, testSRT)
		start, end, err := MiddleSRTRange(path, 300) // 5-minute half-window
		if err != nil {
			t.Fatal(err)
		}
		// Bounds: first=60s, last=1745s. Duration=1685s. Mid=60+842.5=902.5
		// Expected: 902.5-300=602.5, 902.5+300=1202.5
		if start < 600 || start > 605 {
			t.Errorf("start = %v, want ~602.5", start)
		}
		if end < 1200 || end > 1205 {
			t.Errorf("end = %v, want ~1202.5", end)
		}
	})

	t.Run("short file within window", func(t *testing.T) {
		short := `1
00:00:10,000 --> 00:00:15,000
Short file.

2
00:00:30,000 --> 00:00:35,000
Only thirty seconds total.
`
		path := writeTempSRT(t, short)
		start, end, err := MiddleSRTRange(path, 300)
		if err != nil {
			t.Fatal(err)
		}
		// Duration 25s < 600s, so full range returned.
		if start != 10.0 {
			t.Errorf("start = %v, want 10", start)
		}
		if end != 35.0 {
			t.Errorf("end = %v, want 35", end)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, _, err := MiddleSRTRange("/nonexistent/path.srt", 300)
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})
}

func TestExtractSRTTimeRange(t *testing.T) {
	t.Run("basic range", func(t *testing.T) {
		path := writeTempSRT(t, testSRT)
		text, err := ExtractSRTTimeRange(path, 500, 1300)
		if err != nil {
			t.Fatal(err)
		}
		// Should include cues at 600s, 900s, 1200s but not 60s or 1740s.
		if text == "" {
			t.Fatal("expected non-empty text")
		}
		if !strings.Contains(text, "Middle dialogue line one") {
			t.Error("expected 'Middle dialogue line one' in output")
		}
		if !strings.Contains(text, "Middle dialogue line two") {
			t.Error("expected 'Middle dialogue line two' in output")
		}
		if !strings.Contains(text, "Near the end") {
			t.Error("expected 'Near the end' in output")
		}
		if strings.Contains(text, "Hello there") {
			t.Error("should not include cue at 60s")
		}
		if strings.Contains(text, "Final line") {
			t.Error("should not include cue at 1740s")
		}
	})

	t.Run("full file range", func(t *testing.T) {
		path := writeTempSRT(t, testSRT)
		text, err := ExtractSRTTimeRange(path, 0, 9999)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "Hello there") {
			t.Error("expected first cue in full range")
		}
		if !strings.Contains(text, "Final line") {
			t.Error("expected last cue in full range")
		}
	})

	t.Run("empty range", func(t *testing.T) {
		path := writeTempSRT(t, testSRT)
		text, err := ExtractSRTTimeRange(path, 5000, 6000)
		if err != nil {
			t.Fatal(err)
		}
		if text != "" {
			t.Errorf("expected empty string, got %q", text)
		}
	})

	t.Run("ad filtering", func(t *testing.T) {
		srt := `1
00:00:01,000 --> 00:00:05,000
Subtitles by OpenSubtitles.org

2
00:05:00,000 --> 00:05:05,000
Actual dialogue here.
`
		path := writeTempSRT(t, srt)
		text, err := ExtractSRTTimeRange(path, 0, 9999)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "OpenSubtitles") {
			t.Error("ad cue should be filtered out")
		}
		if !strings.Contains(text, "Actual dialogue") {
			t.Error("real dialogue should be included")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := ExtractSRTTimeRange("/nonexistent/path.srt", 0, 100)
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})
}
