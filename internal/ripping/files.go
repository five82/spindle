package ripping

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func existsNonEmptyDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

func refreshWorkingCopy(src, dst string) error {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" {
		return errors.New("refresh working copy: empty path")
	}
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil
	}
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("refresh working copy: remove existing: %w", err)
	}
	return copyDir(src, dst)
}

func mapToWorkingPath(rawPath, rawDir, workingDir string) string {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return rawPath
	}
	if strings.TrimSpace(rawDir) == "" || strings.TrimSpace(workingDir) == "" {
		return rawPath
	}
	rel, err := filepath.Rel(rawDir, rawPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return filepath.Join(workingDir, filepath.Base(rawPath))
	}
	return filepath.Join(workingDir, rel)
}

func copyDir(src, dst string) error {
	if src == "" || dst == "" {
		return errors.New("copyDir: empty path")
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if info.Mode().Type() != 0 {
			return nil
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// selectCachedRip picks the largest MKV in dir, assuming it is the primary
// feature. Returns an empty string when none are present.
func selectCachedRip(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	type candidate struct {
		path string
		size int64
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".mkv") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		candidates = append(candidates, candidate{path: filepath.Join(dir, entry.Name()), size: info.Size()})
	}
	if len(candidates) == 0 {
		return "", nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].size > candidates[j].size
	})
	return candidates[0].path, nil
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return strings.TrimSpace(replacer.Replace(name))
}

func copyPlaceholder(src, dst string) error {
	sourceData, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read source file: %w", err)
	}
	if err := os.WriteFile(dst, sourceData, 0o644); err != nil {
		return fmt.Errorf("write placeholder file: %w", err)
	}
	return nil
}
