package language

import (
	"strings"
)

// entry holds the canonical data for a single supported language.
type entry struct {
	DisplayName string   // Human-readable name (e.g. "English")
	ISO1        string   // ISO 639-1 two-letter code (e.g. "en")
	ISO2Primary string   // ISO 639-2 primary three-letter code (e.g. "eng")
	AltCodes    []string // Alternate three-letter codes (e.g. "fre" for French)
}

// table is the authoritative list of supported languages.
var table = []entry{
	{"English", "en", "eng", nil},
	{"Spanish", "es", "spa", nil},
	{"French", "fr", "fra", []string{"fre"}},
	{"German", "de", "deu", []string{"ger"}},
	{"Italian", "it", "ita", nil},
	{"Portuguese", "pt", "por", nil},
	{"Japanese", "ja", "jpn", nil},
	{"Korean", "ko", "kor", nil},
	{"Chinese", "zh", "zho", []string{"chi"}},
	{"Russian", "ru", "rus", nil},
	{"Arabic", "ar", "ara", nil},
	{"Hindi", "hi", "hin", nil},
	{"Dutch", "nl", "nld", []string{"dut"}},
	{"Polish", "pl", "pol", nil},
	{"Swedish", "sv", "swe", nil},
	{"Danish", "da", "dan", nil},
	{"Norwegian", "no", "nor", []string{"nob", "nno"}},
	{"Finnish", "fi", "fin", nil},
}

// Lookup indexes built once at init.
var (
	// byCode maps any recognized code or word form to an *entry.
	byCode map[string]*entry
)

func init() {
	byCode = make(map[string]*entry, len(table)*4)
	for i := range table {
		e := &table[i]
		byCode[e.ISO1] = e
		byCode[e.ISO2Primary] = e
		for _, alt := range e.AltCodes {
			byCode[alt] = e
		}
		byCode[strings.ToLower(e.DisplayName)] = e
	}
}

// resolve looks up a code (case-insensitive) and returns the entry or nil.
func resolve(code string) *entry {
	return byCode[strings.ToLower(strings.TrimSpace(code))]
}

// ToISO2 converts any recognized code or word form to a two-letter ISO 639-1
// code. Unknown two-letter codes pass through unchanged. Everything else
// returns an empty string.
func ToISO2(code string) string {
	code = strings.TrimSpace(code)
	if e := resolve(code); e != nil {
		return e.ISO1
	}
	if len(code) == 2 {
		return strings.ToLower(code)
	}
	return ""
}

// ToISO3 converts any recognized code or word form to a three-letter ISO 639-2
// primary code. Unknown two-letter codes return "und" (undetermined). Unknown
// three-letter codes pass through unchanged.
func ToISO3(code string) string {
	code = strings.TrimSpace(code)
	if e := resolve(code); e != nil {
		return e.ISO2Primary
	}
	lower := strings.ToLower(code)
	if len(lower) == 3 {
		return lower
	}
	if len(lower) == 2 {
		return "und"
	}
	return ""
}

// DisplayName returns the human-readable name for a recognized code or word
// form. Unrecognized codes are returned uppercased.
func DisplayName(code string) string {
	if e := resolve(code); e != nil {
		return e.DisplayName
	}
	return strings.ToUpper(strings.TrimSpace(code))
}

// ExtractFromTags extracts a language code from stream metadata tags. Keys are
// checked in priority order: language, LANGUAGE, Language, language_ietf, lang,
// LANG. Null bytes are stripped from values. Returns an empty string when no
// language tag is found.
func ExtractFromTags(tags map[string]string) string {
	keys := []string{"language", "LANGUAGE", "Language", "language_ietf", "lang", "LANG"}
	for _, k := range keys {
		if v, ok := tags[k]; ok {
			v = strings.ReplaceAll(v, "\x00", "")
			v = strings.TrimSpace(v)
			if v != "" {
				return v
			}
		}
	}
	return ""
}

// NormalizeList deduplicates and normalizes a slice of language codes to
// ISO 639-1. Codes longer than two characters are converted via ToISO2; codes
// that cannot be resolved are dropped. Order is preserved (first occurrence
// wins).
func NormalizeList(languages []string) []string {
	seen := make(map[string]struct{}, len(languages))
	out := make([]string, 0, len(languages))
	for _, raw := range languages {
		var code string
		if len(strings.TrimSpace(raw)) > 2 {
			code = ToISO2(raw)
		} else {
			code = strings.ToLower(strings.TrimSpace(raw))
		}
		if code == "" {
			continue
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out
}
