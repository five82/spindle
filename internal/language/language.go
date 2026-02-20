package language

import "strings"

type entry struct {
	code2   string   // ISO 639-1 (2-letter)
	code3   string   // ISO 639-2 primary (3-letter)
	alt3    string   // ISO 639-2 alternate (e.g. "fre" vs "fra")
	display string   // Human-readable name
	words   []string // Full word forms (e.g. "english")
}

var languages = []entry{
	{"en", "eng", "", "English", []string{"english"}},
	{"es", "spa", "", "Spanish", []string{"spanish"}},
	{"fr", "fra", "fre", "French", []string{"french"}},
	{"de", "deu", "ger", "German", []string{"german"}},
	{"it", "ita", "", "Italian", []string{"italian"}},
	{"pt", "por", "", "Portuguese", []string{"portuguese"}},
	{"ja", "jpn", "", "Japanese", []string{"japanese"}},
	{"ko", "kor", "", "Korean", []string{"korean"}},
	{"zh", "zho", "chi", "Chinese", []string{"chinese"}},
	{"ru", "rus", "", "Russian", []string{"russian"}},
	{"ar", "ara", "", "Arabic", []string{"arabic"}},
	{"hi", "hin", "", "Hindi", []string{"hindi"}},
	{"nl", "nld", "dut", "Dutch", []string{"dutch"}},
	{"pl", "pol", "", "Polish", []string{"polish"}},
	{"sv", "swe", "", "Swedish", []string{"swedish"}},
	{"da", "dan", "", "Danish", []string{"danish"}},
	{"no", "nor", "", "Norwegian", []string{"norwegian"}},
	{"fi", "fin", "", "Finnish", []string{"finnish"}},
}

// Index maps built at init time.
var (
	byCode2 map[string]*entry
	byCode3 map[string]*entry
	byWord  map[string]*entry
)

func init() {
	byCode2 = make(map[string]*entry, len(languages))
	byCode3 = make(map[string]*entry, len(languages)*2)
	byWord = make(map[string]*entry, len(languages))
	for i := range languages {
		e := &languages[i]
		byCode2[e.code2] = e
		byCode3[e.code3] = e
		if e.alt3 != "" {
			byCode3[e.alt3] = e
		}
		for _, w := range e.words {
			byWord[w] = e
		}
	}
}

func lookup(code string) *entry {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return nil
	}
	if e, ok := byCode2[code]; ok {
		return e
	}
	if e, ok := byCode3[code]; ok {
		return e
	}
	if e, ok := byWord[code]; ok {
		return e
	}
	return nil
}

// ToISO2 converts any recognized language code or word to ISO 639-1 (2-letter).
// Returns empty string for unrecognized input.
// If the input is already a 2-letter code (even if unknown), it passes through.
func ToISO2(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return ""
	}
	if e := lookup(code); e != nil {
		return e.code2
	}
	if len(code) == 2 {
		return code
	}
	return ""
}

// ToISO3 converts any recognized language code to ISO 639-2 (3-letter).
// Returns "und" for unrecognized 2-letter codes, passes through 3-letter codes.
func ToISO3(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return "und"
	}
	if e := lookup(code); e != nil {
		return e.code3
	}
	if len(code) == 3 {
		return code
	}
	return "und"
}

// DisplayName returns a human-readable language name for any recognized code.
// Returns "Unknown" for empty input, or the uppercased code for unrecognized input.
func DisplayName(code string) string {
	if strings.TrimSpace(code) == "" {
		return "Unknown"
	}
	if e := lookup(code); e != nil {
		return e.display
	}
	return strings.ToUpper(strings.TrimSpace(code))
}

// ExtractFromTags extracts and normalizes the language from stream metadata tags.
// Checks common tag keys: language, LANGUAGE, Language, language_ietf, lang, LANG.
func ExtractFromTags(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := []string{"language", "LANGUAGE", "Language", "language_ietf", "lang", "LANG"}
	for _, key := range keys {
		if value, ok := tags[key]; ok {
			value = strings.TrimSpace(strings.ReplaceAll(value, "\u0000", ""))
			if value != "" {
				return strings.ToLower(value)
			}
		}
	}
	return ""
}

// NormalizeList deduplicates and normalizes a list of language codes to ISO 639-1.
func NormalizeList(languages []string) []string {
	if len(languages) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(languages))
	seen := make(map[string]struct{}, len(languages))
	for _, lang := range languages {
		trimmed := strings.ToLower(strings.TrimSpace(lang))
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 2 {
			if mapped := ToISO2(trimmed); mapped != "" {
				trimmed = mapped
			}
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}
