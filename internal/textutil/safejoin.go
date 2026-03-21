package textutil

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SafeJoin joins base and segment using filepath.Join and verifies the result
// does not escape the base directory. Returns an error if the cleaned path
// is outside the base.
func SafeJoin(base, segment string) (string, error) {
	if filepath.IsAbs(segment) {
		return "", fmt.Errorf("path %q escapes base %q", segment, base)
	}
	cleanBase := filepath.Clean(base)
	joined := filepath.Join(cleanBase, segment)
	cleaned := filepath.Clean(joined)
	// The cleaned path must be equal to or under the base directory.
	if cleaned == cleanBase {
		return cleaned, nil
	}
	if !strings.HasPrefix(cleaned, cleanBase+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes base %q", segment, base)
	}
	return cleaned, nil
}
