package ripcache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/queue"
)

const (
	// freeSpaceFloor is the minimum free-space ratio we allow before pruning (e.g., 0.20 => 80% full).
	freeSpaceFloor = 0.20
)

// statfsFunc allows tests to stub filesystem stats.
type statfsFunc func(path string) (total uint64, free uint64, err error)

// Manager handles storing and pruning ripped artifacts.
type Manager struct {
	root     string
	maxBytes int64
	logger   *slog.Logger
	statfs   statfsFunc
}

// Stats describes current cache usage.
type Stats struct {
	Entries        int            `json:"entries"`
	TotalBytes     int64          `json:"total_bytes"`
	MaxBytes       int64          `json:"max_bytes"`
	FreeBytes      uint64         `json:"free_bytes"`
	TotalFSBytes   uint64         `json:"total_fs_bytes"`
	FreeRatio      float64        `json:"free_ratio"`
	EntrySummaries []EntrySummary `json:"entry_summaries"`
}

// EntrySummary surfaces human-friendly details about a rip cache entry so the
// CLI can show which titles are currently stored.
type EntrySummary struct {
	Directory      string    `json:"directory"`
	SizeBytes      int64     `json:"size_bytes"`
	ModifiedAt     time.Time `json:"modified_at"`
	PrimaryFile    string    `json:"primary_file"`
	VideoFileCount int       `json:"video_file_count"`
}

// NewManager builds a cache manager when enabled; returns nil when caching is disabled or misconfigured.
func NewManager(cfg *config.Config, logger *slog.Logger) *Manager {
	if cfg == nil || !cfg.RipCache.Enabled {
		return nil
	}
	root := strings.TrimSpace(cfg.RipCache.Dir)
	if root == "" || cfg.RipCache.MaxGiB <= 0 {
		return nil
	}
	maxBytes := int64(cfg.RipCache.MaxGiB) * 1024 * 1024 * 1024
	manager := &Manager{
		root:     root,
		maxBytes: maxBytes,
		statfs:   realStatfs,
	}
	manager.SetLogger(logger)
	return manager
}

// SetLogger refreshes the manager's logging destination (allows per-item log routing).
func (m *Manager) SetLogger(logger *slog.Logger) {
	if m == nil {
		return
	}
	m.logger = logging.NewComponentLogger(logger, "ripcache")
}

// Store copies a rip directory into the cache and triggers pruning.
// It is primarily used in tests; the normal flow writes directly to cache paths.
func (m *Manager) Store(ctx context.Context, item *queue.Item, ripDir string) error {
	if m == nil || item == nil {
		return nil
	}
	src := strings.TrimSpace(ripDir)
	if src == "" {
		return errors.New("ripcache: empty rip directory")
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("ripcache: inspect rip dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("ripcache: rip path %q is not a directory", src)
	}

	dest := m.cachePath(item)
	// Replace any existing entry for this item.
	if err := os.RemoveAll(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("ripcache: remove existing cache entry: %w", err)
	}
	if err := copyDir(src, dest); err != nil {
		return fmt.Errorf("ripcache: copy entry: %w", err)
	}
	_ = os.Chtimes(dest, time.Now(), time.Now())

	if err := m.prune(ctx, dest); err != nil {
		return fmt.Errorf("ripcache: prune after store: %w", err)
	}
	m.logger.InfoContext(ctx, "stored rip cache entry",
		logging.String("cache_dir", dest),
		logging.String("disc_fingerprint", strings.TrimSpace(item.DiscFingerprint)),
	)
	return nil
}

// Register marks an already-written cache entry and prunes other entries.
// keepPath protects the active entry from deletion; if space cannot be freed
// without removing keepPath, an error is returned.
func (m *Manager) Register(ctx context.Context, item *queue.Item, keepPath string) error {
	if m == nil || item == nil {
		return nil
	}
	path := strings.TrimSpace(keepPath)
	if path == "" {
		return errors.New("ripcache: register path is empty")
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	if err := m.prune(ctx, path); err != nil {
		return err
	}
	return nil
}

// Prune removes entries based on size and free-space thresholds.
// keepPath, when provided, will not be deleted unless it is the sole entry and
// free-space constraints cannot be satisfied.
func (m *Manager) Prune(ctx context.Context, keepPath string) error {
	return m.prune(ctx, keepPath)
}

// Stats returns current cache usage and filesystem free-space info.
func (m *Manager) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	if m == nil {
		return s, nil
	}
	entries, totalSize, err := m.scan()
	if err != nil {
		return s, err
	}
	totalFS, freeFS, err := m.statfs(m.root)
	if err != nil {
		return s, fmt.Errorf("ripcache: statfs: %w", err)
	}
	ratio := 1.0
	if totalFS > 0 {
		ratio = float64(freeFS) / float64(totalFS)
	}
	details := make([]EntrySummary, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		details = append(details, EntrySummary{
			Directory:      entry.path,
			SizeBytes:      entry.sizeBytes,
			ModifiedAt:     entry.modTime,
			PrimaryFile:    entry.primary,
			VideoFileCount: entry.videoCount,
		})
	}
	s = Stats{
		Entries:        len(entries),
		TotalBytes:     totalSize,
		MaxBytes:       m.maxBytes,
		FreeBytes:      freeFS,
		TotalFSBytes:   totalFS,
		FreeRatio:      ratio,
		EntrySummaries: details,
	}
	if len(entries) == 0 {
		m.logger.InfoContext(ctx, "rip cache empty")
	}
	return s, nil
}

// Restore copies a cached rip back into the target directory when missing.
// Returns true when a cache entry was used.
func (m *Manager) Restore(ctx context.Context, item *queue.Item, targetDir string) (bool, error) {
	if m == nil || item == nil {
		return false, nil
	}
	targetDir = strings.TrimSpace(targetDir)
	if targetDir == "" {
		return false, errors.New("ripcache: empty target directory")
	}
	if existsNonEmptyDir(targetDir) {
		return false, nil
	}
	src := m.cachePath(item)
	if !existsNonEmptyDir(src) {
		return false, nil
	}
	if err := copyDir(src, targetDir); err != nil {
		return false, fmt.Errorf("ripcache: restore entry: %w", err)
	}
	now := time.Now()
	_ = os.Chtimes(src, now, now)
	_ = os.Chtimes(targetDir, now, now)

	m.logger.InfoContext(ctx, "restored rip from cache",
		logging.String("cache_dir", src),
		logging.String("target_dir", targetDir),
		logging.String("disc_fingerprint", strings.TrimSpace(item.DiscFingerprint)),
	)
	return true, nil
}

// prune removes oldest cache entries until both size and free-space thresholds are satisfied.
func (m *Manager) prune(ctx context.Context, keepPath string) error {
	entries, totalSize, err := m.scan()
	if err != nil {
		return err
	}

	for len(entries) > 0 {
		freeOK, err := m.freeSpaceOK()
		if err != nil {
			return err
		}
		if totalSize <= m.maxBytes && freeOK {
			return nil
		}
		// Remove oldest entry.
		oldest := entries[0]
		if samePath(oldest.path, keepPath) && len(entries) == 1 {
			// Only the active entry exists; cannot prune further.
			return fmt.Errorf("ripcache: cache over limits and active entry %q cannot be pruned", keepPath)
		}
		if samePath(oldest.path, keepPath) {
			entries = entries[1:]
			continue
		}
		if err := os.RemoveAll(oldest.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("ripcache: remove %q: %w", oldest.path, err)
		}
		m.logger.InfoContext(ctx, "pruned rip cache entry",
			logging.String("cache_dir", oldest.path),
			logging.Int64("entry_size_bytes", oldest.sizeBytes),
		)
		totalSize -= oldest.sizeBytes
		entries = entries[1:]
	}
	return nil
}

type cacheEntry struct {
	path       string
	sizeBytes  int64
	modTime    time.Time
	primary    string
	videoCount int
}

func (m *Manager) scan() ([]cacheEntry, int64, error) {
	entries := make([]cacheEntry, 0)
	var total int64
	rootEntries, err := os.ReadDir(m.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return entries, 0, nil
		}
		return nil, 0, fmt.Errorf("ripcache: list root: %w", err)
	}
	for _, entry := range rootEntries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(m.root, entry.Name())
		size, mtime, err := dirSizeAndTime(path)
		if err != nil {
			m.logger.Warn("ripcache: skip entry; excluded from stats and pruning",
				logging.String("source_file", path),
				logging.Error(err),
				logging.String(logging.FieldEventType, "ripcache_entry_skipped"),
				logging.String(logging.FieldErrorHint, "inspect cache directory permissions or remove the corrupted entry"),
			)
			continue
		}
		primary, count := identifyPrimaryFile(path)
		total += size
		entries = append(entries, cacheEntry{path: path, sizeBytes: size, modTime: mtime, primary: primary, videoCount: count})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime.Before(entries[j].modTime)
	})
	return entries, total, nil
}

var cacheVideoExtensions = map[string]struct{}{
	".mkv": {},
	".mp4": {},
	".m4v": {},
	".mov": {},
	".avi": {},
}

func identifyPrimaryFile(dir string) (string, int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0
	}
	type candidate struct {
		name string
		size int64
	}
	files := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if _, ok := cacheVideoExtensions[ext]; !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, candidate{name: entry.Name(), size: info.Size()})
	}
	if len(files) == 0 {
		return "", 0
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].size == files[j].size {
			return files[i].name < files[j].name
		}
		return files[i].size > files[j].size
	})
	return files[0].name, len(files)
}

func (m *Manager) freeSpaceOK() (bool, error) {
	total, free, err := m.statfs(m.root)
	if err != nil {
		return false, fmt.Errorf("ripcache: statfs: %w", err)
	}
	if total == 0 {
		return true, nil
	}
	ratio := float64(free) / float64(total)
	return ratio >= freeSpaceFloor, nil
}

func (m *Manager) cachePath(item *queue.Item) string {
	segment := strings.TrimSpace(item.DiscFingerprint)
	if segment == "" && item.ID > 0 {
		segment = fmt.Sprintf("queue-%d", item.ID)
	}
	if segment == "" {
		segment = sanitize(item.DiscTitle)
	}
	if segment == "" {
		segment = "queue-temp"
	}
	return filepath.Join(m.root, sanitize(segment))
}

// Path returns the cache directory for the given queue item.
func (m *Manager) Path(item *queue.Item) string {
	if m == nil || item == nil {
		return ""
	}
	return m.cachePath(item)
}

func samePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA == nil {
		a = ra
	}
	if errB == nil {
		b = rb
	}
	return a == b
}

func dirSizeAndTime(path string) (int64, time.Time, error) {
	var (
		size    int64
		latest  time.Time
		visited = false
	)
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		visited = true
		if !info.IsDir() {
			size += info.Size()
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return 0, time.Time{}, err
	}
	if !visited {
		return 0, time.Time{}, errors.New("empty cache entry")
	}
	return size, latest, nil
}

func existsNonEmptyDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
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
			// Skip special files.
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

func sanitize(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		" ", "-",
		":", "-",
		"*", "",
		"?", "",
		"\"", "",
		"<", "",
		">", "",
		"|", "",
	)
	value = replacer.Replace(value)
	value = strings.Trim(value, "-_.")
	if value == "" {
		return "queue"
	}
	return value
}

func realStatfs(path string) (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	return total, free, nil
}
