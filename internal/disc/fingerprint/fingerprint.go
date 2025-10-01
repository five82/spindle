package fingerprint

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var errMountNotFound = errors.New("optical drive mount point not found")

// Compute returns a deterministic fingerprint for the disc mounted from device.
// discType may be "Blu-ray", "DVD", or left empty; when empty the directory
// layout is probed to pick an appropriate strategy.
func Compute(ctx context.Context, device, discType string) (string, error) {
	mountPoint, err := resolveMountPoint(device)
	if err != nil {
		return "", err
	}
	if mountPoint == "" {
		return "", errMountNotFound
	}

	info, err := os.Stat(mountPoint)
	if err != nil {
		return "", fmt.Errorf("stat mount: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("mount point %q is not a directory", mountPoint)
	}

	kind := classifyDisc(mountPoint, discType)

	switch kind {
	case "Blu-ray":
		if fp, err := computeBluRayFingerprint(ctx, mountPoint); err == nil {
			return fp, nil
		} else if !errors.Is(err, errNoMetadata) {
			return "", err
		}
	case "DVD":
		if fp, err := computeDVDFingerprint(ctx, mountPoint); err == nil {
			return fp, nil
		} else if !errors.Is(err, errNoMetadata) {
			return "", err
		}
	}

	// Fallback: hash directory manifest (first 64 KiB of each file, sorted).
	return computeManifestFingerprint(ctx, mountPoint, 64*1024)
}

var errNoMetadata = errors.New("expected metadata files missing")

func computeBluRayFingerprint(ctx context.Context, base string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	idPath := filepath.Join(base, "CERTIFICATE", "id.bdmv")
	if exists(idPath) {
		return hashFileManifest(base, []string{relativePath(base, idPath)}, 0)
	}

	var files []string

	coreFiles := []string{
		filepath.Join("BDMV", "index.bdmv"),
		filepath.Join("BDMV", "MovieObject.bdmv"),
	}
	for _, rel := range coreFiles {
		if exists(filepath.Join(base, rel)) {
			files = append(files, filepath.ToSlash(rel))
		}
	}

	playlistDir := filepath.Join(base, "BDMV", "PLAYLIST")
	if entries, err := os.ReadDir(playlistDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(strings.ToLower(name), ".mpls") {
				rel := filepath.Join("BDMV", "PLAYLIST", name)
				files = append(files, filepath.ToSlash(rel))
			}
		}
	}

	clipInfoDir := filepath.Join(base, "BDMV", "CLIPINF")
	if entries, err := os.ReadDir(clipInfoDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(strings.ToLower(name), ".clpi") {
				rel := filepath.Join("BDMV", "CLIPINF", name)
				files = append(files, filepath.ToSlash(rel))
			}
		}
	}

	if len(files) == 0 {
		return "", errNoMetadata
	}

	sort.Strings(files)
	return hashFileManifest(base, files, 0)
}

func computeDVDFingerprint(ctx context.Context, base string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	videoTS := filepath.Join(base, "VIDEO_TS")
	entries, err := os.ReadDir(videoTS)
	if err != nil {
		return "", errNoMetadata
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(strings.ToLower(name), ".ifo") {
			rel := filepath.Join("VIDEO_TS", name)
			files = append(files, filepath.ToSlash(rel))
		}
	}

	if len(files) == 0 {
		return "", errNoMetadata
	}

	sort.Strings(files)
	return hashFileManifest(base, files, 0)
}

func computeManifestFingerprint(ctx context.Context, base string, maxBytes int64) (string, error) {
	var files []string
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := relativePath(base, path)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", errNoMetadata
	}
	sort.Strings(files)
	return hashFileManifest(base, files, maxBytes)
}

func hashFileManifest(base string, files []string, maxBytes int64) (string, error) {
	h := sha256.New()
	for _, rel := range files {
		abs := filepath.Join(base, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", rel, err)
		}
		if err := appendFileToHash(h, abs, rel, info.Size(), maxBytes); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func appendFileToHash(h hash.Hash, abs, rel string, size int64, maxBytes int64) error {
	_, _ = h.Write([]byte(rel))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.FormatInt(size, 10)))
	_, _ = h.Write([]byte{0})

	file, err := os.Open(abs)
	if err != nil {
		return fmt.Errorf("open %s: %w", rel, err)
	}
	defer file.Close()

	reader := io.Reader(file)
	if maxBytes > 0 && size > maxBytes {
		reader = io.LimitReader(file, maxBytes)
	}
	if _, err := io.Copy(h, reader); err != nil {
		return fmt.Errorf("hash %s: %w", rel, err)
	}
	_, _ = h.Write([]byte{0})
	return nil
}

func relativePath(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}

func exists(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	return false
}

func classifyDisc(mountPoint, hint string) string {
	hint = strings.TrimSpace(strings.ToLower(hint))
	switch hint {
	case "blu-ray", "blu ray", "blu-ray disc", "bd":
		return "Blu-ray"
	case "dvd":
		return "DVD"
	}

	if hasDir(mountPoint, "BDMV") {
		return "Blu-ray"
	}
	if hasDir(mountPoint, "VIDEO_TS") {
		return "DVD"
	}
	return ""
}

func hasDir(base, name string) bool {
	info, err := os.Stat(filepath.Join(base, name))
	return err == nil && info.IsDir()
}

func resolveMountPoint(device string) (string, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return "", fmt.Errorf("open mounts: %w", err)
	}
	defer f.Close()

	requested, _ := filepath.EvalSymlinks(device)
	if requested == "" {
		requested = device
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		mountDevice := decodeMountField(fields[0])
		mountPath := decodeMountField(fields[1])

		canonical, _ := filepath.EvalSymlinks(mountDevice)
		if canonical == "" {
			canonical = mountDevice
		}

		if sameDevice(requested, canonical) {
			return mountPath, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan mounts: %w", err)
	}
	return "", errMountNotFound
}

func decodeMountField(field string) string {
	replacer := strings.NewReplacer(
		"\\040", " ",
		"\\011", "\t",
		"\\012", "\n",
		"\\134", "\\",
	)
	return replacer.Replace(field)
}

func sameDevice(a, b string) bool {
	if a == b {
		return true
	}
	if strings.HasPrefix(a, "/dev/") && strings.HasPrefix(b, "/dev/") {
		return filepath.Base(a) == filepath.Base(b)
	}
	return false
}

// ComputeTimeout wraps Compute with a deadline to avoid blocking indefinitely
// on slow mounts. The default timeout is 30 seconds.
func ComputeTimeout(ctx context.Context, device, discType string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return Compute(ctx, device, discType)
}
