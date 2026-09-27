//go:build linux

package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/five82/spindle/internal/config"
)

func withInteractiveStdin(t *testing.T, input string) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = slave
	t.Cleanup(func() { os.Stdin = oldStdin; _ = slave.Close() })
	if _, err := master.WriteString(input); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmInteractiveTTY(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		wantErr     bool
	}{
		{"yes", "YES\n", false}, {"no", "no\n", true}, {"empty", "\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withInteractiveStdin(t, tc.input)
			if err := confirm("Remove cache?", false); (err != nil) != tc.wantErr {
				t.Fatalf("confirm: %v", err)
			}
		})
	}
}

func TestCacheRipTitleSelectionAfterIdentification(t *testing.T) {
	oldCfg, oldSocket, oldQuiet := cfg, flagSocket, flagQuiet
	t.Cleanup(func() { cfg, flagSocket, flagQuiet = oldCfg, oldSocket, oldQuiet })
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("XDG_RUNTIME_DIR", dir)
	mount := filepath.Join(dir, "disc")
	if err := os.Mkdir(mount, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mount, "disc.txt"), []byte("test disc"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{
		"lsblk":      fmt.Sprintf("#!/bin/sh\necho '{\"blockdevices\":[{\"name\":\"sr0\",\"label\":\"Test Movie\",\"fstype\":\"iso9660\",\"mountpoint\":%q}]}'\n", mount),
		"makemkvcon": "#!/bin/sh\ncase \" $* \" in *' info '*) printf '%s\\n' 'CINFO:2,0,\"Test Movie\"' 'TINFO:1,2,0,\"Feature\"' 'TINFO:1,9,0,\"1:30:00\"' 'TINFO:2,2,0,\"Bonus\"' 'TINFO:2,9,0,\"0:30:00\"';; *) exit 1;; esac\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[{"id":55,"title":"Test Movie","media_type":"movie","release_date":"2020-01-01","vote_average":8,"vote_count":5000}]}`)
	}))
	defer server.Close()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir, StagingDir: filepath.Join(dir, "staging")}, TMDB: config.TMDBConfig{APIKey: "key", BaseURL: server.URL}, MakeMKV: config.MakeMKVConfig{OpticalDrive: "/dev/sr0", InfoTimeout: 10}}
	flagSocket = filepath.Join(dir, "missing.sock")
	for _, tc := range []struct{ name, flag, value, want string }{
		{"invalid title", "title", "9", "not a candidate"},
		{"choose needs tty", "choose", "true", "interactive terminal"},
		{"chosen title reaches rip", "title", "1", "ripping:"},
		{"interactive invalid input", "choose", "true", "invalid title ID"},
		{"interactive unknown title", "choose", "true", "not a candidate"},
		{"interactive selected title", "choose", "true", "ripping:"},
		{"only one eligible title", "choose", "true", "ripping:"},
		{"no eligible titles", "choose", "true", "no titles above minimum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "only one eligible title" {
				cfg.MakeMKV.MinTitleLength = 2000
			}
			if tc.name == "no eligible titles" {
				cfg.MakeMKV.MinTitleLength = 6000
			}
			defer func() { cfg.MakeMKV.MinTitleLength = 0 }()
			cmd := newCacheRipCmd()
			if strings.HasPrefix(tc.name, "interactive") {
				withInteractiveStdin(t, map[string]string{"interactive invalid input": "oops\n", "interactive unknown title": "9\n", "interactive selected title": "1\n"}[tc.name])
			}
			if err := cmd.Flags().Set(tc.flag, tc.value); err != nil {
				t.Fatal(err)
			}
			err := cmd.RunE(cmd, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("cache rip: %v, want %q", err, tc.want)
			}
		})
	}
}
