package transcription

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/five82/spindle/internal/language"
	"github.com/five82/spindle/internal/srtutil"
)

const canonicalSchemaVersion = "qwen3-canonical-v4"

var qwenAlignedLanguageNames = map[string]string{
	"ar": "Arabic",
	"de": "German",
	"en": "English",
	"es": "Spanish",
	"fr": "French",
	"it": "Italian",
	"ja": "Japanese",
	"ko": "Korean",
	"pt": "Portuguese",
	"ru": "Russian",
	"zh": "Chinese",
}

type canonicalWord struct {
	Word        string  `json:"word"`
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Probability float64 `json:"probability,omitempty"`
}

type canonicalSegment struct {
	Start float64         `json:"start"`
	End   float64         `json:"end"`
	Text  string          `json:"text"`
	Words []canonicalWord `json:"words,omitempty"`
}

type canonicalPayload struct {
	Language         string             `json:"language,omitempty"`
	DetectedLanguage string             `json:"detected_language,omitempty"`
	SchemaVersion    string             `json:"schema_version,omitempty"`
	Segments         []canonicalSegment `json:"segments,omitempty"`
}

type transcriptArtifacts struct {
	SRTPath  string
	JSONPath string
	Language string
	Segments int
	Duration float64
}

func supportsAlignedLanguage(code string) bool {
	_, ok := qwenAlignedLanguageNames[language.ToISO2(code)]
	return ok
}

func qwenLanguageName(code string) (string, bool) {
	iso2 := language.ToISO2(code)
	name, ok := qwenAlignedLanguageNames[iso2]
	if ok {
		return name, true
	}
	if iso2 == "" {
		return "", false
	}
	return language.DisplayName(iso2), false
}

func buildTranscriptArtifacts(outputDir, requestedLanguage string, response *workerTranscribeResponse) (*transcriptArtifacts, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create transcription output dir: %w", err)
	}
	iso2 := language.ToISO2(requestedLanguage)
	if workerISO2 := language.ToISO2(response.Language); workerISO2 != "" {
		iso2 = workerISO2
	}
	if iso2 == "" {
		iso2 = "en"
	}
	segments := buildCanonicalSegments(response.Text, response.TimeStamps)
	payload := canonicalPayload{
		Language:         iso2,
		DetectedLanguage: iso2,
		SchemaVersion:    canonicalSchemaVersion,
		Segments:         segments,
	}
	jsonPath := filepath.Join(outputDir, "audio.json")
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical transcript json: %w", err)
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("write canonical transcript json: %w", err)
	}
	cues := make([]srtutil.Cue, 0, len(segments))
	for i, segment := range segments {
		cues = append(cues, srtutil.Cue{
			Index: i + 1,
			Start: segment.Start,
			End:   segment.End,
			Text:  segment.Text,
		})
	}
	srtPath := filepath.Join(outputDir, "audio.srt")
	if err := os.WriteFile(srtPath, []byte(srtutil.Format(cues)), 0o644); err != nil {
		return nil, fmt.Errorf("write canonical transcript srt: %w", err)
	}
	duration := 0.0
	if len(segments) > 0 {
		duration = segments[len(segments)-1].End
	}
	return &transcriptArtifacts{
		SRTPath:  srtPath,
		JSONPath: jsonPath,
		Language: iso2,
		Segments: len(segments),
		Duration: duration,
	}, nil
}

func buildCanonicalSegments(text string, stamps []workerTimeStamp) []canonicalSegment {
	if len(stamps) == 0 {
		return []canonicalSegment{syntheticSegment(text)}
	}
	segments := make([]canonicalSegment, 0, len(stamps)/6+1)
	var current canonicalSegment
	flush := func() {
		if len(current.Words) == 0 {
			return
		}
		current.Text = normalizeSegmentText(current.Text)
		if current.Text == "" {
			current.Text = normalizeSegmentText(joinWords(current.Words))
		}
		segments = append(segments, current)
		current = canonicalSegment{}
	}
	for i, stamp := range stamps {
		word := canonicalWord{
			Word:        stamp.Text,
			Start:       stamp.StartTime,
			End:         stamp.EndTime,
			Probability: 1.0,
		}
		if word.End < word.Start {
			word.End = word.Start
		}
		if len(current.Words) == 0 {
			current = canonicalSegment{Start: word.Start, End: word.End, Words: []canonicalWord{word}, Text: strings.TrimSpace(stamp.Text)}
			continue
		}
		prev := current.Words[len(current.Words)-1]
		gap := word.Start - prev.End
		shouldSplit := gap >= 0.65 || (word.End-current.Start) >= 6.0 || len(current.Words) >= 12 || endsStrongSentence(prev.Word)
		if shouldSplit {
			flush()
			current = canonicalSegment{Start: word.Start, End: word.End, Words: []canonicalWord{word}, Text: strings.TrimSpace(stamp.Text)}
			continue
		}
		current.End = word.End
		current.Words = append(current.Words, word)
		current.Text = appendToken(current.Text, stamp.Text)
		if i == len(stamps)-1 {
			current.End = word.End
		}
	}
	flush()
	if len(segments) == 0 {
		return []canonicalSegment{syntheticSegment(text)}
	}
	projectCanonicalSegmentText(text, segments)
	for i := range segments {
		if segments[i].End <= segments[i].Start {
			segments[i].End = segments[i].Start + 0.5
		}
	}
	return segments
}

func projectCanonicalSegmentText(rawText string, segments []canonicalSegment) {
	rawText = normalizeWorkerText(rawText)
	if rawText == "" || len(segments) == 0 {
		return
	}
	words := parseProjectedWords(rawText)
	if len(words) == 0 {
		return
	}
	wordIndex := 0
	for i := range segments {
		startSearch := wordIndex
		segmentStart := -1
		for _, word := range segments[i].Words {
			norm := normalizeProjectedWord(word.Word)
			if norm == "" {
				continue
			}
			match := -1
			for j := wordIndex; j < len(words); j++ {
				if words[j].Norm == norm {
					match = j
					break
				}
			}
			if match == -1 {
				segmentStart = -1
				wordIndex = startSearch
				break
			}
			if segmentStart == -1 {
				segmentStart = match
			}
			wordIndex = match + 1
		}
		if segmentStart == -1 {
			continue
		}
		end := len(rawText)
		if wordIndex < len(words) {
			end = words[wordIndex].Start
		}
		projected := strings.TrimSpace(rawText[words[segmentStart].Start:end])
		if projected != "" {
			segments[i].Text = normalizeSegmentText(projected)
		}
	}
}

type projectedWord struct {
	Start int
	Norm  string
}

func parseProjectedWords(text string) []projectedWord {
	words := make([]projectedWord, 0, len(strings.Fields(text)))
	inWord := false
	start := 0
	for i, r := range text {
		if isProjectedWordRune(r) {
			if !inWord {
				start = i
				inWord = true
			}
			continue
		}
		if inWord {
			if norm := normalizeProjectedWord(text[start:i]); norm != "" {
				words = append(words, projectedWord{Start: start, Norm: norm})
			}
			inWord = false
		}
	}
	if inWord {
		if norm := normalizeProjectedWord(text[start:]); norm != "" {
			words = append(words, projectedWord{Start: start, Norm: norm})
		}
	}
	return words
}

func isProjectedWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’' || r == '-'
}

func normalizeProjectedWord(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func syntheticSegment(text string) canonicalSegment {
	clean := normalizeSegmentText(text)
	if clean == "" {
		clean = "[unintelligible]"
	}
	words := strings.Fields(clean)
	estimated := 2.0
	if n := len(words); n > 0 {
		estimated = float64(n) * 0.45
		if estimated < 2.0 {
			estimated = 2.0
		}
	}
	return canonicalSegment{Start: 0, End: estimated, Text: clean}
}

func normalizeSegmentText(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func joinWords(words []canonicalWord) string {
	text := ""
	for _, word := range words {
		text = appendToken(text, word.Word)
	}
	return text
}

func appendToken(text, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return text
	}
	if text == "" {
		return token
	}
	if shouldInsertSpace(text, token) {
		return text + " " + token
	}
	return text + token
}

func shouldInsertSpace(text, token string) bool {
	last, _ := utf8.DecodeLastRuneInString(text)
	first, _ := utf8.DecodeRuneInString(token)
	if unicode.IsSpace(last) || unicode.IsSpace(first) {
		return false
	}
	if strings.ContainsRune(",.!?;:%)]}", first) {
		return false
	}
	if strings.ContainsRune("([{#$", last) {
		return false
	}
	return true
}

func endsStrongSentence(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(text)
	return r == '.' || r == '!' || r == '?'
}

func normalizeWorkerText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	var b strings.Builder
	lastSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if lastSpace {
				continue
			}
			b.WriteByte(' ')
			lastSpace = true
			continue
		}
		lastSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
