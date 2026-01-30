package encoding

import (
	"io"
	"os"
	"path/filepath"
	"strings"

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
	if err := copyFile(sourcePath, desiredPath); err != nil {
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

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
