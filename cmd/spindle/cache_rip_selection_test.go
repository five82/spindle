package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestCacheRipTitleSelectionWithDiscFixture(t *testing.T) {
	oldCfg, oldSocket, oldQuiet := cfg, flagSocket, flagQuiet
	t.Cleanup(func() { cfg, flagSocket, flagQuiet = oldCfg, oldSocket, oldQuiet })
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	flagSocket = filepath.Join(dir, "absent.sock")
	flagQuiet = false
	mount := filepath.Join(dir, "mount")
	if err := os.Mkdir(mount, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mount, "disc.txt"), []byte("feature"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/search/") {
			t.Errorf("unexpected TMDB path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"results":[{"id":55,"title":"Test Movie","media_type":"movie","release_date":"2020-01-01","vote_average":8,"vote_count":5000,"overview":"An operator test movie."},{"id":56,"title":"Other Movie","media_type":"movie","release_date":"2018-01-01","vote_average":6,"vote_count":30}]}`))
	}))
	defer server.Close()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir, StagingDir: filepath.Join(dir, "staging")}, MakeMKV: config.MakeMKVConfig{OpticalDrive: "disc:0", InfoTimeout: 10, RipTimeout: 10, MinTitleLength: 60}, TMDB: config.TMDBConfig{APIKey: "key", BaseURL: server.URL, Language: "en-US"}, RipCache: config.RipCacheConfig{MaxGiB: 1}}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{
		"lsblk":      fmt.Sprintf("#!/bin/sh\necho '{\"blockdevices\":[{\"name\":\"sr0\",\"label\":\"Test Movie\",\"mountpoint\":%q}]}'\n", mount),
		"makemkvcon": "#!/bin/sh\ncase \"$3\" in\n info) printf '%s\\n' 'CINFO:2,0,\"Test Movie\"' 'TINFO:1,2,0,\"Feature\"' 'TINFO:1,9,0,\"1:30:00\"'\n if [ \"$SCAN_MODE\" = multi ]; then printf '%s\\n' 'TINFO:2,2,0,\"Alternate\"' 'TINFO:2,9,0,\"1:28:00\"'; fi;;\n mkv) for arg do case \"$arg\" in */ripped) truncate -s 10485761 \"$arg/feature_t01.mkv\";; esac; done\n printf '%s\\n' 'MSG:5036,0,2,\"Copy complete\",\"%1 titles saved, %2 failed\",1,0';;\nesac\n",
		"ffprobe":    "#!/bin/sh\nprintf '%s\\n' '{\"streams\":[{\"codec_type\":\"video\"},{\"codec_type\":\"audio\"}],\"format\":{\"duration\":\"120\"}}'\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	identify := newIdentifyCmd()
	identified := captureStdout(t, func() {
		if err := identify.RunE(identify, []string{"/dev/sr0"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"=== Disc Info ===", "Feature", "Fingerprint:", "=== TMDB Search ===", "Selected:", "Test Movie", "Overview:", "Other candidates (1):", "Other Movie"} {
		if !strings.Contains(identified, want) {
			t.Fatalf("identify output missing %q: %s", want, identified)
		}
	}
	invalid := newCacheRipCmd()
	if err := invalid.Flags().Set("title", "99"); err != nil {
		t.Fatal(err)
	}
	if err := invalid.RunE(invalid, []string{"/dev/sr0"}); err == nil || !strings.Contains(err.Error(), "not a candidate") {
		t.Fatalf("invalid title: %v", err)
	}
	cfg.MakeMKV.MinTitleLength = 6000
	short := newCacheRipCmd()
	if err := short.Flags().Set("choose", "true"); err != nil {
		t.Fatal(err)
	}
	if err := short.RunE(short, []string{"/dev/sr0"}); err == nil || !strings.Contains(err.Error(), "no titles above minimum") {
		t.Fatalf("short titles: %v", err)
	}
	cfg.MakeMKV.MinTitleLength = 60
	t.Setenv("SCAN_MODE", "multi")
	many := newCacheRipCmd()
	if err := many.Flags().Set("choose", "true"); err != nil {
		t.Fatal(err)
	}
	if err := many.RunE(many, []string{"/dev/sr0"}); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("multiple titles without terminal: %v", err)
	}
	t.Setenv("SCAN_MODE", "")
	choose := newCacheRipCmd()
	if err := choose.Flags().Set("choose", "true"); err != nil {
		t.Fatal(err)
	}
	got := captureStdout(t, func() {
		if err := choose.RunE(choose, []string{"/dev/sr0"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "Only one candidate title") || !strings.Contains(got, "Cached disc:") {
		t.Fatalf("output: %s", got)
	}
}
