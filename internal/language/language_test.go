package language

import (
	"reflect"
	"testing"
)

func TestToISO2(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// Recognized two-letter codes.
		{"iso1 english", "en", "en"},
		{"iso1 japanese", "ja", "ja"},

		// Three-letter primary codes.
		{"iso2 english", "eng", "en"},
		{"iso2 spanish", "spa", "es"},
		{"iso2 japanese", "jpn", "ja"},

		// Alternate three-letter codes.
		{"alt french", "fre", "fr"},
		{"alt german", "ger", "de"},
		{"alt chinese", "chi", "zh"},
		{"alt dutch", "dut", "nl"},
		{"alt norwegian nob", "nob", "no"},
		{"alt norwegian nno", "nno", "no"},

		// Word forms (case-insensitive).
		{"word english", "english", "en"},
		{"word English", "English", "en"},
		{"word FRENCH", "FRENCH", "fr"},
		{"word finnish", "finnish", "fi"},

		// Unknown two-letter codes pass through.
		{"unknown iso1", "xx", "xx"},
		{"unknown iso1 caps", "QQ", "qq"},

		// Everything else returns empty.
		{"unknown iso2", "xyz", ""},
		{"unknown word", "klingon", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToISO2(tt.input)
			if got != tt.want {
				t.Errorf("ToISO2(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestToISO3(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// Recognized codes.
		{"from iso1", "en", "eng"},
		{"from iso2", "fra", "fra"},
		{"from alt", "fre", "fra"},
		{"from word", "german", "deu"},
		{"from alt ger", "ger", "deu"},
		{"norwegian", "no", "nor"},

		// Unknown two-letter returns "und".
		{"unknown iso1", "xx", "und"},

		// Unknown three-letter passes through.
		{"unknown iso2", "xyz", "xyz"},
		{"unknown iso2 caps", "XYZ", "xyz"},

		// Longer unknown returns empty.
		{"unknown word", "klingon", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToISO3(tt.input)
			if got != tt.want {
				t.Errorf("ToISO3(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDisplayName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"from iso1", "en", "English"},
		{"from iso2", "spa", "Spanish"},
		{"from alt", "fre", "French"},
		{"from word", "japanese", "Japanese"},
		{"case insensitive", "KOREAN", "Korean"},

		// Unknown codes are uppercased.
		{"unknown short", "xx", "XX"},
		{"unknown long", "xyz", "XYZ"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DisplayName(tt.input)
			if got != tt.want {
				t.Errorf("DisplayName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractFromTags(t *testing.T) {
	tests := []struct {
		name string
		tags map[string]string
		want string
	}{
		{
			"language key",
			map[string]string{"language": "eng", "LANGUAGE": "spa"},
			"eng",
		},
		{
			"LANGUAGE key",
			map[string]string{"LANGUAGE": "spa"},
			"spa",
		},
		{
			"Language key",
			map[string]string{"Language": "fra"},
			"fra",
		},
		{
			"language_ietf key",
			map[string]string{"language_ietf": "en-US"},
			"en-US",
		},
		{
			"lang key",
			map[string]string{"lang": "de"},
			"de",
		},
		{
			"LANG key",
			map[string]string{"LANG": "ja"},
			"ja",
		},
		{
			"null byte stripping",
			map[string]string{"language": "eng\x00\x00"},
			"eng",
		},
		{
			"empty value skipped",
			map[string]string{"language": "", "LANGUAGE": "por"},
			"por",
		},
		{
			"whitespace only skipped",
			map[string]string{"language": "  ", "lang": "it"},
			"it",
		},
		{
			"empty tags",
			map[string]string{},
			"",
		},
		{
			"nil tags",
			nil,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractFromTags(tt.tags)
			if got != tt.want {
				t.Errorf("ExtractFromTags(%v) = %q, want %q", tt.tags, got, tt.want)
			}
		})
	}
}

func TestNormalizeList(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			"dedup",
			[]string{"en", "en", "fr"},
			[]string{"en", "fr"},
		},
		{
			"normalize iso2 to iso1",
			[]string{"eng", "fra"},
			[]string{"en", "fr"},
		},
		{
			"mixed codes",
			[]string{"en", "spa", "fre", "de", "japanese"},
			[]string{"en", "es", "fr", "de", "ja"},
		},
		{
			"dedup after normalization",
			[]string{"en", "eng", "english"},
			[]string{"en"},
		},
		{
			"unknown long codes dropped",
			[]string{"en", "klingon"},
			[]string{"en"},
		},
		{
			"unknown short codes kept",
			[]string{"xx", "en"},
			[]string{"xx", "en"},
		},
		{
			"empty input",
			[]string{},
			[]string{},
		},
		{
			"nil input",
			nil,
			[]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeList(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NormalizeList(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
