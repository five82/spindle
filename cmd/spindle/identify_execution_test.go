package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestIdentifyCommandPrintsDiscAndNoMatch(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	bin := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' 'CINFO:2,0,"Test Film"' 'TINFO:1,2,0,"Feature"' 'TINFO:1,9,0,"1:30:00"' 'TINFO:1,10,0,"15000000000"'
`
	if err := os.WriteFile(filepath.Join(bin, "makemkvcon"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lsblk", "mount"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	matched := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/search/") {
			t.Errorf("unexpected TMDB path %s", r.URL.Path)
		}
		if matched {
			_, _ = w.Write([]byte(`{"results":[{"id":42,"title":"Test Film","release_date":"2001-01-01","media_type":"movie","vote_count":1000,"vote_average":8.1,"overview":"A short synopsis."},{"id":43,"title":"Test Film Sequel","release_date":"2004-01-01","media_type":"movie","vote_count":100}]}`))
		} else {
			_, _ = w.Write([]byte(`{"results":[]}`))
		}
	}))
	defer server.Close()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: t.TempDir()}, MakeMKV: config.MakeMKVConfig{OpticalDrive: "disc:0", InfoTimeout: 5}, TMDB: config.TMDBConfig{APIKey: "test", BaseURL: server.URL, Language: "en-US"}}
	cmd := newIdentifyCmd()
	output := captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"Scanning disc", "Disc Info", "Test Film", "Feature", "TMDB Search", "No TMDB results met confidence threshold"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q from output: %s", want, output)
		}
	}
	matched = true
	output = captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"Selected:", "Test Film (2001)", "Other candidates (1):", "Test Film Sequel"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q from matched output: %s", want, output)
		}
	}
}
