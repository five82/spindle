package language

import (
	"testing"
)

func TestToISO2(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// 2-letter codes pass through
		{"en", "en"},
		{"EN", "en"},
		{"es", "es"},
		// 3-letter codes convert
		{"eng", "en"},
		{"spa", "es"},
		{"fra", "fr"},
		{"fre", "fr"},
		{"deu", "de"},
		{"ger", "de"},
		{"ita", "it"},
		{"por", "pt"},
		{"jpn", "ja"},
		{"kor", "ko"},
		{"zho", "zh"},
		{"chi", "zh"},
		{"rus", "ru"},
		{"ara", "ar"},
		{"hin", "hi"},
		{"nld", "nl"},
		{"dut", "nl"},
		{"pol", "pl"},
		{"swe", "sv"},
		{"dan", "da"},
		{"nor", "no"},
		{"fin", "fi"},
		// Word forms
		{"english", "en"},
		{"French", "fr"},
		{"GERMAN", "de"},
		{"chinese", "zh"},
		// Unknown 2-letter passes through
		{"xy", "xy"},
		// Unknown 3-letter returns empty
		{"xyz", ""},
		// Empty
		{"", ""},
		{" ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ToISO2(tt.input)
			if result != tt.expected {
				t.Errorf("ToISO2(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestToISO3(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"en", "eng"},
		{"es", "spa"},
		{"fr", "fra"},
		{"de", "deu"},
		{"zh", "zho"},
		{"eng", "eng"},
		{"spa", "spa"},
		{"xyz", "xyz"}, // unknown 3-letter passes through
		{"xy", "und"},  // unknown 2-letter becomes undefined
		{"", "und"},    // empty
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ToISO3(tt.input)
			if result != tt.expected {
				t.Errorf("ToISO3(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestDisplayName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"en", "English"},
		{"eng", "English"},
		{"es", "Spanish"},
		{"spa", "Spanish"},
		{"fr", "French"},
		{"fre", "French"},
		{"fra", "French"},
		{"de", "German"},
		{"deu", "German"},
		{"ger", "German"},
		{"ja", "Japanese"},
		{"ko", "Korean"},
		{"zh", "Chinese"},
		{"chi", "Chinese"},
		{"zho", "Chinese"},
		{"nl", "Dutch"},
		{"dut", "Dutch"},
		{"nld", "Dutch"},
		{"", "Unknown"},
		{"xyz", "XYZ"},
		{"english", "English"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := DisplayName(tt.input)
			if result != tt.expected {
				t.Errorf("DisplayName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractFromTags(t *testing.T) {
	tests := []struct {
		name     string
		tags     map[string]string
		expected string
	}{
		{"nil tags", nil, ""},
		{"empty tags", map[string]string{}, ""},
		{"lowercase key", map[string]string{"language": "eng"}, "eng"},
		{"uppercase key", map[string]string{"LANGUAGE": "ENG"}, "eng"},
		{"lang key", map[string]string{"lang": "en"}, "en"},
		{"LANG key", map[string]string{"LANG": "EN"}, "en"},
		{"ietf key", map[string]string{"language_ietf": "en-US"}, "en-us"},
		{"null bytes stripped", map[string]string{"language": "eng\x00"}, "eng"},
		{"empty value", map[string]string{"language": ""}, ""},
		{"priority: language over LANG", map[string]string{"language": "fr", "LANG": "en"}, "fr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractFromTags(tt.tags)
			if result != tt.expected {
				t.Errorf("ExtractFromTags(%v) = %q, want %q", tt.tags, result, tt.expected)
			}
		})
	}
}

func TestNormalizeList(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{"nil", nil, nil},
		{"empty", []string{}, nil},
		{"single", []string{"en"}, []string{"en"}},
		{"dedup", []string{"en", "en"}, []string{"en"}},
		{"normalize 3-letter", []string{"eng", "spa"}, []string{"en", "es"}},
		{"mixed", []string{"en", "eng", "fr", "fra"}, []string{"en", "fr"}},
		{"unknown passes through", []string{"en", "xx"}, []string{"en", "xx"}},
		{"strips whitespace", []string{" en ", " "}, []string{"en"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeList(tt.input)
			if len(result) != len(tt.expected) {
				t.Fatalf("NormalizeList(%v) = %v, want %v", tt.input, result, tt.expected)
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("NormalizeList(%v)[%d] = %q, want %q", tt.input, i, result[i], tt.expected[i])
				}
			}
		})
	}
}
