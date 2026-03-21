package subtitle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSRTContent_Valid(t *testing.T) {
	srt := `1
00:00:01,000 --> 00:00:03,000
Hello

2
00:00:05,000 --> 00:00:07,000
World

3
00:00:10,000 --> 00:00:12,000
Test
`
	path := writeTempSRT(t, srt)
	issues, err := ValidateSRTContent(path, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Errorf("expected no issues, got %v", issues)
	}
}

func TestValidateSRTContent_EmptyFile(t *testing.T) {
	path := writeTempSRT(t, "")
	issues, err := ValidateSRTContent(path, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0] != "empty_subtitle_file" {
		t.Errorf("expected empty_subtitle_file, got %v", issues)
	}
}

func TestValidateSRTContent_DurationMismatch(t *testing.T) {
	srt := `1
00:00:01,000 --> 00:00:03,000
Hello

2
00:02:00,000 --> 00:02:10,000
World
`
	path := writeTempSRT(t, srt)
	// Video is 60s but last cue ends at 130s.
	issues, err := ValidateSRTContent(path, 60)
	if err != nil {
		t.Fatal(err)
	}
	if !containsIssue(issues, "duration_mismatch") {
		t.Errorf("expected duration_mismatch, got %v", issues)
	}
}

func TestValidateSRTContent_SparseSubtitles(t *testing.T) {
	srt := `1
00:00:01,000 --> 00:00:03,000
Only cue
`
	path := writeTempSRT(t, srt)
	// 1 cue in 120s video = 0.5 cues/min < 2.
	issues, err := ValidateSRTContent(path, 120)
	if err != nil {
		t.Fatal(err)
	}
	if !containsIssue(issues, "sparse_subtitles") {
		t.Errorf("expected sparse_subtitles, got %v", issues)
	}
}

func TestValidateSRTContent_LateFirstCue(t *testing.T) {
	srt := `1
00:20:00,000 --> 00:20:05,000
Late start
`
	path := writeTempSRT(t, srt)
	issues, err := ValidateSRTContent(path, 1800)
	if err != nil {
		t.Fatal(err)
	}
	if !containsIssue(issues, "late_first_cue") {
		t.Errorf("expected late_first_cue, got %v", issues)
	}
}

func TestValidateSRTContent_MultipleIssues(t *testing.T) {
	srt := `1
00:16:00,000 --> 00:50:00,000
Only cue starting very late
`
	path := writeTempSRT(t, srt)
	// Video 120s, last cue at 3000s (duration_mismatch),
	// 1 cue / 2min (sparse), first cue at 960s (late_first_cue).
	issues, err := ValidateSRTContent(path, 120)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) < 2 {
		t.Errorf("expected multiple issues, got %v", issues)
	}
}

func writeTempSRT(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.srt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsIssue(issues []string, target string) bool {
	for _, issue := range issues {
		if issue == target {
			return true
		}
	}
	return false
}
