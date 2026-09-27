package ripper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestRipRunMissingTargetsAfterCacheMiss(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	env := ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "unknown"}}
	item.RipSpecData, err = env.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateWorkState(item); err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}}, nil, ripcache.New(t.TempDir(), 1), nil, NoTitleOverride)
	if err := h.Run(context.Background(), sess); err == nil || !strings.Contains(err.Error(), "cannot select rip targets") {
		t.Fatalf("Run: %v", err)
	}
}

func TestRestoreTitlesFromCachedEnvelopeCases(t *testing.T) {
	h := &Handler{}
	cached := ripspec.Envelope{Version: ripspec.CurrentVersion, Titles: []ripspec.Title{{ID: 3, Name: "Feature"}}}
	data, err := cached.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		initial []ripspec.Title
		data    string
		want    string
	}{
		{"restored", nil, data, "Feature"},
		{"existing", []ripspec.Title{{ID: 1, Name: "Current"}}, data, "Current"},
		{"invalid", nil, "broken", ""},
		{"empty", nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := &ripspec.Envelope{Titles: tc.initial}
			h.restoreTitlesFromCachedEnvelope(testLogger(), env, tc.data)
			got := ""
			if len(env.Titles) > 0 {
				got = env.Titles[0].Name
			}
			if got != tc.want {
				t.Fatalf("titles: %+v", env.Titles)
			}
		})
	}
}

func TestDiscoverRippedFileAndDriveInfo(t *testing.T) {
	dir := t.TempDir()
	h := &Handler{}
	if _, err := h.discoverNewRippedFile(testLogger(), dir, 7, nil); err == nil || !strings.Contains(err.Error(), "no new mkv") {
		t.Fatalf("missing output: %v", err)
	}
	file := filepath.Join(dir, "title.mkv")
	if err := os.WriteFile(file, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := h.discoverNewRippedFile(testLogger(), dir, 7, nil)
	if err != nil || found != file {
		t.Fatalf("found %q: %v", found, err)
	}
	vendor, model := driveInfo(dir)
	if vendor != "" || model != "" {
		t.Fatalf("non-device drive: %s %s", vendor, model)
	}
	vendor, model = driveInfo("/dev/not-a-physical-drive-spindle-test")
	if vendor != "" || model != "" {
		t.Fatalf("missing sysfs identity: %q %q", vendor, model)
	}
}
