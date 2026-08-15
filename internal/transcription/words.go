package transcription

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Word is one wav2vec2-aligned word timestamp from a transcript audio.json.
type Word struct {
	Text  string
	Start float64 // seconds
	End   float64 // seconds
}

// ReadAlignedWords extracts the per-word forced-alignment timestamps from a
// transcript audio.json written by the WhisperX wrapper. Words the aligner
// could not time (numerals, out-of-vocabulary tokens) are skipped. The result
// is sorted by start time.
func ReadAlignedWords(jsonPath string) ([]Word, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Segments []struct {
			Words []struct {
				Word  string   `json:"word"`
				Start *float64 `json:"start"`
				End   *float64 `json:"end"`
			} `json:"words"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse transcript json: %w", err)
	}
	var words []Word
	for _, segment := range payload.Segments {
		for _, w := range segment.Words {
			text := strings.TrimSpace(w.Word)
			if w.Start == nil || w.End == nil || text == "" {
				continue
			}
			words = append(words, Word{Text: text, Start: *w.Start, End: *w.End})
		}
	}
	sort.Slice(words, func(i, j int) bool { return words[i].Start < words[j].Start })
	return words, nil
}
