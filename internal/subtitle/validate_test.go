package subtitle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/srtutil"
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

func TestValidateSRTContent_LineFormattingIssues(t *testing.T) {
	t.Run("too_many_lines", func(t *testing.T) {
		path := writeTempSRT(t, "1\n00:00:01,000 --> 00:00:04,000\nLine one\nLine two\nLine three\n")
		issues, err := ValidateSRTContent(path, 30)
		if err != nil {
			t.Fatal(err)
		}
		if !containsIssue(issues, "too_many_lines") {
			t.Fatalf("expected too_many_lines, got %v", issues)
		}
	})

	t.Run("line_too_long", func(t *testing.T) {
		path := writeTempSRT(t, "1\n00:00:01,000 --> 00:00:04,000\nThis line is deliberately written to exceed the configured subtitle width limit.\n")
		issues, err := ValidateSRTContent(path, 30)
		if err != nil {
			t.Fatal(err)
		}
		if !containsIssue(issues, "line_too_long") {
			t.Fatalf("expected line_too_long, got %v", issues)
		}
	})

	t.Run("unbalanced_line_breaks", func(t *testing.T) {
		path := writeTempSRT(t, "1\n00:00:01,000 --> 00:00:04,000\nThis line is much longer than\nshort\n")
		issues, err := ValidateSRTContent(path, 30)
		if err != nil {
			t.Fatal(err)
		}
		if !containsIssue(issues, "unbalanced_line_breaks") {
			t.Fatalf("expected unbalanced_line_breaks, got %v", issues)
		}
	})

	t.Run("high_reading_speed", func(t *testing.T) {
		path := writeTempSRT(t, "1\n00:00:01,000 --> 00:00:01,500\nThis subtitle has far too many characters for half a second.\n")
		issues, err := ValidateSRTContent(path, 30)
		if err != nil {
			t.Fatal(err)
		}
		if !containsIssue(issues, "high_reading_speed") {
			t.Fatalf("expected high_reading_speed, got %v", issues)
		}
	})

	t.Run("cue_duration_issues", func(t *testing.T) {
		path := writeTempSRT(t, "1\n00:00:01,000 --> 00:00:01,300\nShort\n\n2\n00:00:05,000 --> 00:00:13,500\nLong enough to flag\n")
		issues, err := ValidateSRTContent(path, 30)
		if err != nil {
			t.Fatal(err)
		}
		if !containsIssue(issues, "short_cue_duration") {
			t.Fatalf("expected short_cue_duration, got %v", issues)
		}
		if !containsIssue(issues, "long_cue_duration") {
			t.Fatalf("expected long_cue_duration, got %v", issues)
		}
	})

	t.Run("overlapping_cues", func(t *testing.T) {
		path := writeTempSRT(t, "1\n00:00:01,000 --> 00:00:03,000\nFirst\n\n2\n00:00:02,500 --> 00:00:04,000\nSecond\n")
		issues, err := ValidateSRTContent(path, 30)
		if err != nil {
			t.Fatal(err)
		}
		if !containsIssue(issues, "overlapping_cues") {
			t.Fatalf("expected overlapping_cues, got %v", issues)
		}
	})
}

func TestValidateSRTContent_Boundaries(t *testing.T) {
	tests := []struct {
		name       string
		srt        string
		duration   float64
		wantIssues []string // nil means expect no issues
	}{
		{
			name: "duration_exactly_plus_8s_no_issue",
			// 4 cues, last ends at 38s, video = 30s, difference = 8.0 exactly. Threshold is > 8.
			// 4 cues / 0.5 min = 8 cues/min — well above sparse threshold.
			srt:        "1\n00:00:01,000 --> 00:00:03,000\nA\n\n2\n00:00:05,000 --> 00:00:07,000\nB\n\n3\n00:00:10,000 --> 00:00:12,000\nC\n\n4\n00:00:36,000 --> 00:00:38,000\nD\n",
			duration:   30,
			wantIssues: nil,
		},
		{
			name: "duration_just_over_8s_flagged",
			// 4 cues, last ends at 38.001s, video = 30s, difference = 8.001. Threshold is > 8.
			srt:        "1\n00:00:01,000 --> 00:00:03,000\nA\n\n2\n00:00:05,000 --> 00:00:07,000\nB\n\n3\n00:00:10,000 --> 00:00:12,000\nC\n\n4\n00:00:36,000 --> 00:00:38,001\nD\n",
			duration:   30,
			wantIssues: []string{"duration_mismatch"},
		},
		{
			name: "exactly_2_cues_per_min_no_sparse",
			srt:  "1\n00:00:01,000 --> 00:00:03,000\nA\n\n2\n00:00:10,000 --> 00:00:12,000\nB\n\n3\n00:00:20,000 --> 00:00:22,000\nC\n\n4\n00:00:30,000 --> 00:00:32,000\nD\n",
			// 4 cues in 120s = 2.0 cues/min. Threshold is < 2.
			duration:   120,
			wantIssues: nil,
		},
		{
			name: "60s_video_sparse_check_skipped",
			srt:  "1\n00:00:01,000 --> 00:00:03,000\nOnly cue\n",
			// 1 cue in 60s = 1 cue/min, but sparse check only applies when > 60s.
			duration:   60,
			wantIssues: nil,
		},
		{
			name: "first_cue_exactly_900s_no_late",
			// First cue at 900.0s. Threshold is > 900.
			// 60+ cues needed for 1800s video at >= 2 cues/min. Use 61 cues starting at 900s.
			srt: func() string {
				var b strings.Builder
				for i := 1; i <= 61; i++ {
					start := 900 + (i-1)*14
					end := start + 3
					fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i,
						srtutil.FormatTimestamp(float64(start)), srtutil.FormatTimestamp(float64(end)),
						fmt.Sprintf("Line %d", i))
				}
				return b.String()
			}(),
			duration:   1800,
			wantIssues: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempSRT(t, tt.srt)
			issues, err := ValidateSRTContent(path, tt.duration)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantIssues == nil {
				if len(issues) != 0 {
					t.Errorf("expected no issues, got %v", issues)
				}
				return
			}
			for _, want := range tt.wantIssues {
				if !containsIssue(issues, want) {
					t.Errorf("expected issue %q in %v", want, issues)
				}
			}
		})
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
