package textutil

import (
	"fmt"
	"strings"
)

// excerptEdge is how much of each end of a subprocess's output survives.
// Failures carry their signal at both ends: a Go panic or cgo crash states
// the fault first, while CLI tools print their fatal error last.
const excerptEdge = 2048

// Excerpt bounds subprocess output for error messages and logs, keeping the
// head and tail of long output so neither end's diagnosis is lost while
// progress spam in the middle cannot flood item errors or notifications.
func Excerpt(output []byte) string {
	s := strings.TrimSpace(string(output))
	if len(s) <= 2*excerptEdge {
		return s
	}
	omitted := len(s) - 2*excerptEdge
	return fmt.Sprintf("%s\n...[%d bytes omitted]...\n%s", s[:excerptEdge], omitted, s[len(s)-excerptEdge:])
}
