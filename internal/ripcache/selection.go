package ripcache

import (
	"fmt"
	"path/filepath"
)

// SelectPrimaryVideo returns the largest detected video file in a directory.
func SelectPrimaryVideo(dir string) (string, int, error) {
	primary, count := identifyPrimaryFile(dir)
	if primary == "" || count == 0 {
		return "", 0, fmt.Errorf("no video files found in %q", dir)
	}
	return filepath.Join(dir, primary), count, nil
}
