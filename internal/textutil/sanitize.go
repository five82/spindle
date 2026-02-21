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

// SanitizeToken converts a string to a lowercase filesystem-safe token.
// Letters are lowercased, digits and hyphens/underscores are kept, everything
// else becomes an underscore. Returns "unknown" for empty input.
func SanitizeToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if out == "" {
		return "unknown"
	}
	return out
}
