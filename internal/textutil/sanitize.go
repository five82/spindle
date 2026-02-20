package textutil

import "strings"

// fileNameReplacer replaces filesystem-unsafe characters with safe alternatives.
var fileNameReplacer = strings.NewReplacer(
	"/", "-",
	"\\", "-",
	":", "-",
	"*", "-",
	"?", "",
	"\"", "",
	"<", "",
	">", "",
	"|", "",
)

// SanitizeFileName replaces filesystem-unsafe characters in a filename.
// Slashes, backslashes, colons, and asterisks become dashes; other unsafe
// characters are removed. The result is trimmed of leading/trailing whitespace.
func SanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return strings.TrimSpace(fileNameReplacer.Replace(name))
}
