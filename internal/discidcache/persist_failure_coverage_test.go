package discidcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCachePersistenceRejectsBlockedPaths(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "cache.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, want string
		prepare          func() error
	}{
		{"parent is file", filepath.Join(blocker, "cache.json"), "create cache dir", nil},
		{"temporary path is directory", filepath.Join(dir, "tmp-blocked.json"), "write tmp", func() error { return os.Mkdir(filepath.Join(dir, "tmp-blocked.json.tmp"), 0o755) }},
		{"destination is directory", filepath.Join(dir, "dest-blocked.json"), "rename tmp", func() error { return os.Mkdir(filepath.Join(dir, "dest-blocked.json"), 0o755) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.prepare != nil {
				if err := tc.prepare(); err != nil {
					t.Fatal(err)
				}
			}
			store.path = tc.path
			if err := store.Set("disc", Entry{TMDBID: 1}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Set: %v, want %s", err, tc.want)
			}
		})
	}
}
