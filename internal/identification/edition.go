package identification

import (
	"context"
	"regexp"
	"strings"

	"spindle/internal/services/llm"
)

// editionPattern maps a regex pattern to its normalized edition label.
type editionPattern struct {
	pattern *regexp.Regexp
	label   string
}

// editionDef defines an edition type with patterns for detection and stripping.
// detectPattern matches the edition anywhere in text (uses word boundaries).
// stripSuffix matches the edition as a suffix for removal (anchored at end).
type editionDef struct {
	label         string
	detectPattern string
	stripSuffix   string
}

// editionDefs is the single source of truth for edition patterns.
// Each entry defines how to detect and strip a particular edition type.
var editionDefs = []editionDef{
	// Director's versions - match with or without full phrase
	{"Director's Cut", `DIRECTOR['S]*\s*(CUT|EDITION|VERSION)`, `DIRECTOR['']?S?\s*(CUT|EDITION|VERSION)`},
	{"Director's Cut", `DIRECTOR['S]*`, ``}, // Standalone detection only

	// Extended versions
	{"Extended Edition", `EXTENDED\s*(CUT|EDITION|VERSION)`, `EXTENDED\s*(CUT|EDITION|VERSION)?`},
	{"Extended Edition", `EXTENDED`, ``}, // Standalone detection only

	// Unrated/Uncut
	{"Unrated", `UNRATED\s*(CUT|EDITION|VERSION)?`, `UNRATED\s*(CUT|EDITION|VERSION)?`},
	{"Uncut", `UNCUT\s*(EDITION|VERSION)?`, `UNCUT\s*(EDITION|VERSION)?`},

	// Theatrical (explicitly marked indicates other versions exist)
	{"Theatrical", `THEATRICAL\s*(CUT|EDITION|VERSION|RELEASE)`, `THEATRICAL\s*(CUT|EDITION|VERSION|RELEASE)?`},

	// Remastered
	{"Remastered", `REMASTERED\s*(EDITION|VERSION)?`, `REMASTERED\s*(EDITION|VERSION)?`},

	// Special editions
	{"Special Edition", `SPECIAL\s*EDITION`, `SPECIAL\s*EDITION`},

	// Anniversary editions
	{"Anniversary Edition", `\d+\s*(TH|ST|ND|RD)?\s*ANNIVERSARY\s*(EDITION)?`, `\d+\s*(TH|ST|ND|RD)?\s*ANNIVERSARY\s*(EDITION)?`},

	// Ultimate/Definitive
	{"Ultimate Edition", `ULTIMATE\s*(CUT|EDITION)`, `ULTIMATE\s*(CUT|EDITION)`},
	{"Definitive Edition", `DEFINITIVE\s*(CUT|EDITION)`, `DEFINITIVE\s*(CUT|EDITION)`},

	// Final Cut
	{"Final Cut", `FINAL\s*CUT`, `FINAL\s*CUT`},

	// Redux
	{"Redux", `REDUX`, `REDUX`},

	// IMAX
	{"IMAX", `IMAX\s*(EDITION)?`, `IMAX\s*(EDITION)?`},
}

// knownEditionPatterns contains compiled regex patterns for edition detection.
// Built from editionDefs at init time.
var knownEditionPatterns []editionPattern

// editionStripPatterns contains compiled regex patterns for stripping edition suffixes.
// Built from editionDefs at init time.
var editionStripPatterns []*regexp.Regexp

func init() {
	for _, def := range editionDefs {
		if def.detectPattern != "" {
			pattern := regexp.MustCompile(`(?i)\b(` + def.detectPattern + `)\b`)
			knownEditionPatterns = append(knownEditionPatterns, editionPattern{pattern, def.label})
		}
		if def.stripSuffix != "" {
			pattern := regexp.MustCompile(`(?i)\s*[-:]\s*` + def.stripSuffix + `\s*$`)
			editionStripPatterns = append(editionStripPatterns, pattern)
		}
	}
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
	s = strings.ReplaceAll(s, "(", "")
	s = strings.ReplaceAll(s, ")", "")
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

