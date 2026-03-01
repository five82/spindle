package encoding

import (
	"os"
	"path/filepath"
	"strings"

	"spindle/internal/fileutil"
	"spindle/internal/services"
)

func ensureEncodedOutput(tempPath, desiredPath, sourcePath string) (string, error) {
	desiredPath = strings.TrimSpace(desiredPath)
	if desiredPath == "" {
		desiredPath = tempPath
	}
	if tempPath != "" {
		if strings.EqualFold(tempPath, desiredPath) {
			return tempPath, nil
		}
		if err := os.Rename(tempPath, desiredPath); err != nil {
			return "", services.Wrap(
				services.ErrTransient,
				"encoding",
				"finalize output",
				"Failed to move encoded artifact into destination",
				err,
			)
		}
		return desiredPath, nil
	}
	if err := fileutil.CopyFile(sourcePath, desiredPath); err != nil {
		return "", services.Wrap(
			services.ErrTransient,
			"encoding",
			"stage placeholder",
			"Failed to stage encoded artifact",
			err,
		)
	}
	return desiredPath, nil
}

func deriveEncodedFilename(rippedPath string) string {
	base := filepath.Base(rippedPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = "encoded"
	}
	return stem + ".mkv"
}
