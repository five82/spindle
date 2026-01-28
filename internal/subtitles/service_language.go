package subtitles

import (
	"path/filepath"
	"strings"
)

func normalizeWhisperLanguage(language string) string {
	lang := strings.TrimSpace(strings.ToLower(language))
	if len(lang) == 2 {
		return lang
	}
	if len(lang) == 3 {
		switch lang {
		case "eng":
			return "en"
		case "spa":
			return "es"
		case "fra":
			return "fr"
		case "ger", "deu":
			return "de"
		case "ita":
			return "it"
		case "por":
			return "pt"
		case "dut", "nld":
			return "nl"
		case "rus":
			return "ru"
		case "jpn":
			return "ja"
		case "kor":
			return "ko"
		}
	}
	return ""
}

func normalizeLanguageList(languages []string) []string {
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
			if mapped := normalizeWhisperLanguage(trimmed); mapped != "" {
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

func baseNameWithoutExt(path string) string {
	filename := filepath.Base(strings.TrimSpace(path))
	if filename == "" || filename == "." {
		return "subtitle"
	}
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

func inferLanguage(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := []string{"language", "LANGUAGE", "lang", "LANG"}
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
