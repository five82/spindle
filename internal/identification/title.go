package identification

import (
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func deriveTitle(sourcePath string) string {
	if sourcePath == "" {
		return DefaultDiscTitle
	}
	base := filepath.Base(sourcePath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	cleaned := strings.Builder{}
	prevSpace := false
	for _, r := range base {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			cleaned.WriteRune(r)
			prevSpace = false
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '.':
			if !prevSpace {
				cleaned.WriteRune(' ')
				prevSpace = true
			}
		}
	}
	title := strings.TrimSpace(cleaned.String())
	if title == "" {
		title = DefaultDiscTitle
	}
	return cases.Title(language.Und).String(title)
}
