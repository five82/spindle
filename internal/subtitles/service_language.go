package subtitles

import (
	"path/filepath"
	"strings"
)

func baseNameWithoutExt(path string) string {
	filename := filepath.Base(strings.TrimSpace(path))
	if filename == "" || filename == "." {
		return "subtitle"
	}
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}
