package identify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/tmdb"
)

// A fake MakeMKV and an unmounted device let the whole identification stage
// exercise its real scan, search, and persistence path without optical media.
func TestScanDiscPrefersMountedStructureAndBDInfo(t *testing.T) {
	bin := t.TempDir()
	mount := t.TempDir()
	if err := os.Mkdir(filepath.Join(mount, "BDMV"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{
		"lsblk":      fmt.Sprintf("#!/bin/sh\necho '{\"blockdevices\":[{\"name\":\"sr0\",\"fstype\":\"iso9660\",\"mountpoint\":%q}]}'\n", mount),
		"bd_info":    "#!/bin/sh\necho 'Disc ID: DISC123'\necho 'Disc Name: Blu Movie (2020)'\n",
		"makemkvcon": "#!/bin/sh\necho 'CINFO:2,0,\"Blu Movie\"'\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := New(&config.Config{MakeMKV: config.MakeMKVConfig{OpticalDrive: "/dev/fake-sr0", InfoTimeout: 10}}, nil, nil, nil)
	result, err := h.scanDisc(context.Background(), &queue.Item{}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if result.DiscSource != "bluray" || result.BDInfo == nil || result.BDInfo.DiscID != "DISC123" || result.DiscInfo.Name != "Blu Movie" {
		t.Fatalf("scan result: %+v", result)
	}
}

func TestIdentificationStageWithUnprobedDisc(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "makemkvcon"), []byte("#!/bin/sh\necho 'CINFO:2,0,\"Test Movie\"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, matched := range []bool{true, false} {
		name := "no_match"
		if matched {
			name = "matched"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, "/search/") {
					t.Errorf("unexpected TMDB path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if matched {
					_, _ = io.WriteString(w, `{"results":[{"id":55,"title":"Test Movie","media_type":"movie","release_date":"2020-01-01","vote_average":8,"vote_count":5000}]}`)
				} else {
					_, _ = io.WriteString(w, `{"results":[]}`)
				}
			}))
			defer server.Close()
			dir := t.TempDir()
			store, err := queue.Open(filepath.Join(dir, "queue.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			item, err := store.NewDisc("Test Movie", "fingerprint")
			if err != nil {
				t.Fatal(err)
			}
			sess, err := stage.NewSession(context.Background(), store, item, nil)
			if err != nil {
				t.Fatal(err)
			}
			sess.Logger = discardLogger()
			cfg := &config.Config{Paths: config.PathsConfig{StagingDir: filepath.Join(dir, "staging")}, MakeMKV: config.MakeMKVConfig{
				OpticalDrive: "/dev/does-not-exist", InfoTimeout: 10,
			}}
			h := New(cfg, tmdb.New("key", server.URL, "en-US", discardLogger()), nil, nil)
			err = h.Run(context.Background(), sess)
			if matched && err != nil || !matched && (err == nil || !strings.Contains(err.Error(), "identification fatal")) {
				t.Fatalf("Run matched=%v: %v", matched, err)
			}
			if sess.Env.Metadata.ID != map[bool]int{true: 55, false: 0}[matched] {
				t.Fatalf("wrong envelope: %+v", sess.Env.Metadata)
			}
			persisted, err := store.GetByID(item.ID)
			if err != nil || persisted.RipSpecData == "" || persisted.MetadataJSON == "" {
				t.Fatalf("identification not persisted: %+v %v", persisted, err)
			}
		})
	}
}
