package identification

import (
	"context"
	"regexp"
	"strings"

	"spindle/internal/services/llm"
)

// EditionDetectionPrompt is the system prompt sent to the LLM when classifying
// whether a disc is an alternate movie edition.
const EditionDetectionPrompt = `You determine if a disc is an alternate movie edition (not the standard theatrical release).

Alternate editions include:
- Director's Cut / Director's Edition
- Extended Edition / Extended Cut
- Unrated / Uncut versions
- Special Editions
- Remastered versions
- Anniversary Editions
- Theatrical vs different cuts
- Color versions of originally B&W films
- Black and white versions (like "Noir" editions)
- IMAX editions

NOT alternate editions:
- Standard theatrical releases
- Different regional releases of the same version
- 4K/UHD remasters (unless labeled as a different cut)
- Bonus disc content
- Just year differences in release date

Respond ONLY with JSON: {"is_edition": true/false, "confidence": 0.0-1.0, "reason": "brief explanation"}`

// EditionDecision represents the LLM's classification of whether a disc is an edition.
type EditionDecision struct {
	IsEdition  bool    `json:"is_edition"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// editionPattern maps a regex pattern to its normalized edition label.
type editionPattern struct {
	pattern *regexp.Regexp
	label   string
}

// knownEditionPatterns contains regex patterns for common edition indicators.
// Order matters: more specific patterns should come before general ones.
var knownEditionPatterns = []editionPattern{
	// Director's versions
	{regexp.MustCompile(`(?i)\b(DIRECTOR['S]*\s*(CUT|EDITION|VERSION))\b`), "Director's Cut"},
	{regexp.MustCompile(`(?i)\bDIRECTOR['S]*\b`), "Director's Cut"},

	// Extended versions
	{regexp.MustCompile(`(?i)\b(EXTENDED\s*(CUT|EDITION|VERSION))\b`), "Extended Edition"},
	{regexp.MustCompile(`(?i)\bEXTENDED\b`), "Extended Edition"},

	// Unrated/Uncut
	{regexp.MustCompile(`(?i)\b(UNRATED\s*(CUT|EDITION|VERSION)?)\b`), "Unrated"},
	{regexp.MustCompile(`(?i)\b(UNCUT\s*(EDITION|VERSION)?)\b`), "Uncut"},

	// Theatrical (when explicitly marked, indicates there are other versions)
	{regexp.MustCompile(`(?i)\b(THEATRICAL\s*(CUT|EDITION|VERSION|RELEASE))\b`), "Theatrical"},

	// Remastered
	{regexp.MustCompile(`(?i)\b(REMASTERED\s*(EDITION|VERSION)?)\b`), "Remastered"},

	// Special editions
	{regexp.MustCompile(`(?i)\b(SPECIAL\s*EDITION)\b`), "Special Edition"},

	// Anniversary editions
	{regexp.MustCompile(`(?i)\b(\d+\s*(TH|ST|ND|RD)?\s*ANNIVERSARY\s*(EDITION)?)\b`), "Anniversary Edition"},

	// Ultimate/Definitive
	{regexp.MustCompile(`(?i)\b(ULTIMATE\s*(CUT|EDITION))\b`), "Ultimate Edition"},
	{regexp.MustCompile(`(?i)\b(DEFINITIVE\s*(CUT|EDITION))\b`), "Definitive Edition"},

	// Final Cut
	{regexp.MustCompile(`(?i)\b(FINAL\s*CUT)\b`), "Final Cut"},

	// Redux
	{regexp.MustCompile(`(?i)\bREDUX\b`), "Redux"},

	// IMAX
	{regexp.MustCompile(`(?i)\bIMAX\s*(EDITION)?\b`), "IMAX"},
}

// editionStripPatterns are patterns to remove from titles before TMDB search.
// These are broader than knownEditionPatterns to catch variations.
var editionStripPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\s*[-:]\s*DIRECTOR['']?S?\s*(CUT|EDITION|VERSION)\s*$`),
	regexp.MustCompile(`(?i)\s*[-:]\s*EXTENDED\s*(CUT|EDITION|VERSION)?\s*$`),
	regexp.MustCompile(`(?i)\s*[-:]\s*UNRATED\s*(CUT|EDITION|VERSION)?\s*$`),
	regexp.MustCompile(`(?i)\s*[-:]\s*UNCUT\s*(EDITION|VERSION)?\s*$`),
	regexp.MustCompile(`(?i)\s*[-:]\s*THEATRICAL\s*(CUT|EDITION|VERSION|RELEASE)?\s*$`),
	regexp.MustCompile(`(?i)\s*[-:]\s*REMASTERED\s*(EDITION|VERSION)?\s*$`),
	regexp.MustCompile(`(?i)\s*[-:]\s*SPECIAL\s*EDITION\s*$`),
	regexp.MustCompile(`(?i)\s*[-:]\s*\d+\s*(TH|ST|ND|RD)?\s*ANNIVERSARY\s*(EDITION)?\s*$`),
	regexp.MustCompile(`(?i)\s*[-:]\s*ULTIMATE\s*(CUT|EDITION)\s*$`),
	regexp.MustCompile(`(?i)\s*[-:]\s*DEFINITIVE\s*(CUT|EDITION)\s*$`),
	regexp.MustCompile(`(?i)\s*[-:]\s*FINAL\s*CUT\s*$`),
	regexp.MustCompile(`(?i)\s*[-:]\s*REDUX\s*$`),
	regexp.MustCompile(`(?i)\s*[-:]\s*IMAX\s*(EDITION)?\s*$`),
}

// StripEditionSuffix removes edition markers from a title for TMDB search.
// For example: "Star Trek: The Motion Picture - Director's Edition" becomes
// "Star Trek: The Motion Picture".
func StripEditionSuffix(title string) string {
	result := strings.TrimSpace(title)
	for _, pattern := range editionStripPatterns {
		result = strings.TrimSpace(pattern.ReplaceAllString(result, ""))
	}
	return result
}

// ExtractKnownEdition checks the disc title against known edition patterns.
// Returns the normalized edition label and true if a match is found.
func ExtractKnownEdition(discTitle string) (string, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(discTitle))
	if normalized == "" {
		return "", false
	}

	// Replace underscores with spaces for matching
	normalized = strings.ReplaceAll(normalized, "_", " ")

	for _, ep := range knownEditionPatterns {
		if ep.pattern.MatchString(normalized) {
			return ep.label, true
		}
	}
	return "", false
}

// DetectEditionWithLLM uses an LLM to determine if a disc is an alternate edition.
// Returns the decision from the LLM. If the LLM call fails, returns an empty decision
// with an error.
func DetectEditionWithLLM(ctx context.Context, client *llm.Client, discTitle, tmdbTitle string) (EditionDecision, error) {
	if client == nil {
		return EditionDecision{}, nil
	}

	userPrompt := buildEditionPrompt(discTitle, tmdbTitle)
	response, err := client.CompleteJSON(ctx, EditionDetectionPrompt, userPrompt)
	if err != nil {
		return EditionDecision{}, err
	}

	var decision EditionDecision
	if err := llm.DecodeLLMJSON(response, &decision); err != nil {
		return EditionDecision{}, err
	}

	// Clamp confidence to valid range [0, 1]
	decision.Confidence = max(0, min(1, decision.Confidence))

	return decision, nil
}

// buildEditionPrompt constructs the user message for edition detection.
func buildEditionPrompt(discTitle, tmdbTitle string) string {
	return "Disc: " + strings.TrimSpace(discTitle) + "\nTMDB: " + strings.TrimSpace(tmdbTitle)
}

// ExtractEditionLabel extracts the edition label from the difference between
// the disc title and the TMDB title. For example, if disc is "LOGAN_NOIR" and
// TMDB is "Logan", this extracts "Noir".
func ExtractEditionLabel(discTitle, tmdbTitle string) string {
	disc := normalizeForEditionComparison(discTitle)
	tmdb := normalizeForEditionComparison(tmdbTitle)

	if disc == "" || tmdb == "" {
		return ""
	}

	// Find what's in disc but not in tmdb
	discWords := strings.Fields(disc)
	tmdbWords := strings.Fields(tmdb)

	tmdbSet := make(map[string]bool)
	for _, w := range tmdbWords {
		tmdbSet[w] = true
	}

	var extra []string
	for _, w := range discWords {
		if !tmdbSet[w] && !isCommonWord(w) && !isYearLike(w) {
			extra = append(extra, w)
		}
	}

	if len(extra) == 0 {
		return ""
	}

	return NormalizeEditionLabel(strings.Join(extra, " "))
}

// normalizeForEditionComparison prepares a title for word comparison in edition detection.
func normalizeForEditionComparison(title string) string {
	// Replace underscores with spaces
	s := strings.ReplaceAll(title, "_", " ")
	// Remove common punctuation
	s = strings.ReplaceAll(s, "'", "")
	s = strings.ReplaceAll(s, "-", " ")
	// Uppercase for comparison
	s = strings.ToUpper(s)
	// Collapse whitespace
	return strings.Join(strings.Fields(s), " ")
}

// commonWords contains words that shouldn't be considered edition markers.
var commonWords = map[string]bool{
	"THE": true, "A": true, "AN": true, "OF": true, "AND": true,
	"IN": true, "TO": true, "FOR": true, "ON": true, "AT": true,
	"BY": true, "WITH": true, "FROM": true, "OR": true,
}

// isCommonWord returns true for words that shouldn't be considered edition markers.
func isCommonWord(word string) bool {
	return commonWords[strings.ToUpper(word)]
}

// isYearLike returns true if the word looks like a year (4 digits starting with 1 or 2).
func isYearLike(word string) bool {
	if len(word) != 4 {
		return false
	}
	if word[0] != '1' && word[0] != '2' {
		return false
	}
	for _, c := range word {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// NormalizeEditionLabel cleans up a raw edition label.
// Converts underscores to spaces and applies title case.
func NormalizeEditionLabel(raw string) string {
	// Replace underscores with spaces and split into words
	s := strings.ReplaceAll(raw, "_", " ")
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}

	// Apply title case to each word
	for i, w := range words {
		words[i] = strings.ToUpper(string(w[0])) + strings.ToLower(w[1:])
	}
	return strings.Join(words, " ")
}

// HasAmbiguousEditionMarker returns true if the disc title contains extra content
// beyond the TMDB title that might indicate an edition, but doesn't match known patterns.
func HasAmbiguousEditionMarker(discTitle, tmdbTitle string) bool {
	// First check if it matches known patterns - if so, not ambiguous
	if _, found := ExtractKnownEdition(discTitle); found {
		return false
	}

	// Check if disc has extra words beyond TMDB title
	label := ExtractEditionLabel(discTitle, tmdbTitle)
	return label != ""
}
