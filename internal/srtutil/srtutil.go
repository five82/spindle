// Package srtutil provides a minimal SRT (SubRip) parser/formatter shared by
// the packages that need to read or write subtitle cues — subtitle filtering,
// transcription, OpenSubtitles reference comparison, and content-ID
// fingerprinting. It intentionally does not do tag/HTML stripping; callers
// that need that should run opensubtitles.CleanSRT ahead of Parse.
package srtutil

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Cue is a single SRT cue.
type Cue struct {
	Index int     // 1-based; 0 when unknown
	Start float64 // seconds
	End   float64 // seconds
	Text  string  // may contain embedded "\n" for multi-line cues
}

// Parse parses SRT content into cues. Malformed blocks are skipped silently —
// this mirrors the behavior of every caller we replace.
func Parse(content string) []Cue {
	var cues []Cue
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")

	i := 0
	for i < len(lines) {
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		if i >= len(lines) {
			break
		}

		idx, err := strconv.Atoi(strings.TrimSpace(lines[i]))
		if err != nil {
			i++
			continue
		}
		i++
		if i >= len(lines) {
			break
		}

		start, end, ok := parseTimingLine(lines[i])
		if !ok {
			i++
			continue
		}
		i++

		var textLines []string
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			textLines = append(textLines, lines[i])
			i++
		}

		cues = append(cues, Cue{
			Index: idx,
			Start: start,
			End:   end,
			Text:  strings.Join(textLines, "\n"),
		})
	}
	return cues
}

// ParseFile reads path and parses its contents. Returns an error only on I/O
// failure; a file that contains zero valid cues yields (nil, nil).
func ParseFile(path string) ([]Cue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(data)), nil
}

// Format renders cues back to SRT text. Callers are responsible for setting
// Index values if they want 1-based renumbering.
func Format(cues []Cue) string {
	var b strings.Builder
	for i, cue := range cues {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%d\n", cue.Index)
		fmt.Fprintf(&b, "%s --> %s\n", FormatTimestamp(cue.Start), FormatTimestamp(cue.End))
		b.WriteString(cue.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// PlainText joins cue texts with single spaces, flattening any embedded
// newlines in multi-line cues. Suitable for fingerprinting and LLM prompts.
func PlainText(cues []Cue) string {
	var b strings.Builder
	for _, cue := range cues {
		text := strings.ReplaceAll(cue.Text, "\n", " ")
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(text)
	}
	return b.String()
}

// ParseTimestamp converts an SRT timestamp ("HH:MM:SS,mmm") to seconds.
// Returns 0 on malformed input, matching callers that treat failures as
// "unknown".
func ParseTimestamp(s string) float64 {
	s = strings.TrimSpace(s)
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		return 0
	}
	hours, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	minutes, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	secParts := strings.SplitN(parts[2], ",", 2)
	if len(secParts) != 2 {
		return 0
	}
	secs, err := strconv.Atoi(secParts[0])
	if err != nil {
		return 0
	}
	millis, err := strconv.Atoi(secParts[1])
	if err != nil {
		return 0
	}
	return float64(hours)*3600 + float64(minutes)*60 + float64(secs) + float64(millis)/1000
}

// FormatTimestamp converts seconds to an SRT timestamp ("HH:MM:SS,mmm").
func FormatTimestamp(secs float64) string {
	if secs < 0 {
		secs = 0
	}
	total := int(secs * 1000)
	ms := total % 1000
	total /= 1000
	s := total % 60
	total /= 60
	m := total % 60
	h := total / 60
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

func parseTimingLine(line string) (start, end float64, ok bool) {
	idx := strings.Index(line, "-->")
	if idx < 0 {
		return 0, 0, false
	}
	left := strings.TrimSpace(line[:idx])
	right := strings.TrimSpace(line[idx+3:])
	if left == "" || right == "" {
		return 0, 0, false
	}
	return ParseTimestamp(left), ParseTimestamp(right), true
}
