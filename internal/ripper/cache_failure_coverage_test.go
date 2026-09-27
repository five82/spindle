package ripper

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
)

func TestCacheFreshRipFailureLeavesNoUnusableEntry(t *testing.T) {
	sess := ripCoverageSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie"}})
	dir := t.TempDir()
	ripped := filepath.Join(dir, "ripped")
	if err := os.Mkdir(ripped, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ripped, "film.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "cache")
	cache := ripcache.New(root, 1)
	h := New(&config.Config{}, nil, cache, nil, NoTitleOverride)
	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))
	// Register succeeds, but a pre-existing directory at the metadata path
	// prevents the atomic rename. The incomplete entry must be removed.
	metaDir := filepath.Join(root, sess.Item.DiscFingerprint, "spindle.cache.json")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	h.cacheFreshRip(logger, sess, ripped, 1)
	if cache.HasCache(sess.Item.DiscFingerprint) {
		t.Fatal("incomplete entry remained after metadata failure")
	}
	if !strings.Contains(out.String(), "rip cache metadata write failed") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.cacheFreshRip(logger, sess, ripped, 1)
	if !strings.Contains(out.String(), "rip cache write failed") {
		t.Fatal(out.String())
	}
}
