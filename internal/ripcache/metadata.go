package ripcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"spindle/internal/queue"
)

const (
	metadataVersion  = 1
	metadataFileName = "spindle.cache.json"
)

// EntryMetadata captures identification metadata for a cached rip.
type EntryMetadata struct {
	Version         int    `json:"version"`
	DiscTitle       string `json:"disc_title,omitempty"`
	DiscFingerprint string `json:"disc_fingerprint,omitempty"`
	RipSpecData     string `json:"rip_spec_data,omitempty"`
	MetadataJSON    string `json:"metadata_json,omitempty"`
	NeedsReview     bool   `json:"needs_review,omitempty"`
	ReviewReason    string `json:"review_reason,omitempty"`
}

// WriteMetadata stores identification metadata alongside the cached rip entry.
func (m *Manager) WriteMetadata(ctx context.Context, item *queue.Item, cacheDir string) error {
	if m == nil || item == nil {
		return nil
	}
	cacheDir = strings.TrimSpace(cacheDir)
	if cacheDir == "" {
		return errors.New("ripcache: metadata cache dir is empty")
	}
	meta := EntryMetadata{
		Version:         metadataVersion,
		DiscTitle:       strings.TrimSpace(item.DiscTitle),
		DiscFingerprint: strings.TrimSpace(item.DiscFingerprint),
		RipSpecData:     strings.TrimSpace(item.RipSpecData),
		MetadataJSON:    strings.TrimSpace(item.MetadataJSON),
		NeedsReview:     item.NeedsReview,
		ReviewReason:    strings.TrimSpace(item.ReviewReason),
	}
	payload, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("ripcache: encode metadata: %w", err)
	}
	target := metadataPath(cacheDir)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("ripcache: ensure cache dir: %w", err)
	}
	tmp := filepath.Join(cacheDir, fmt.Sprintf(".spindle-cache-%d.tmp", time.Now().UnixNano()))
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return fmt.Errorf("ripcache: write metadata temp: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ripcache: rename metadata: %w", err)
	}
	if m.logger != nil {
		m.logger.DebugContext(ctx, "stored rip cache metadata", "cache_dir", cacheDir)
	}
	return nil
}

// LoadMetadata reads cached identification metadata for a rip cache entry.
func LoadMetadata(cacheDir string) (EntryMetadata, bool, error) {
	cacheDir = strings.TrimSpace(cacheDir)
	if cacheDir == "" {
		return EntryMetadata{}, false, errors.New("ripcache: metadata cache dir is empty")
	}
	path := metadataPath(cacheDir)
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return EntryMetadata{}, false, nil
		}
		return EntryMetadata{}, false, fmt.Errorf("ripcache: read metadata: %w", err)
	}
	var meta EntryMetadata
	if err := json.Unmarshal(payload, &meta); err != nil {
		return EntryMetadata{}, true, fmt.Errorf("ripcache: decode metadata: %w", err)
	}
	if meta.Version != metadataVersion {
		return EntryMetadata{}, true, fmt.Errorf("ripcache: unsupported metadata version %d", meta.Version)
	}
	return meta, true, nil
}

func metadataPath(cacheDir string) string {
	return filepath.Join(cacheDir, metadataFileName)
}
