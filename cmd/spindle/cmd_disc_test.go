package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/gofrs/flock"
	"github.com/spf13/cobra"
)

func TestDiscScanCommandInventory(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	dir := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' 'CINFO:2,0,"Example Disc"' 'TINFO:0,2,0,"Main Feature"' 'TINFO:0,8,0,"12"' 'TINFO:0,9,0,"1:20:00"' 'TINFO:0,10,0,"1234567"' 'TINFO:0,16,0,"00800.mpls"' 'TINFO:0,25,0,"2"'
`
	if err := os.WriteFile(filepath.Join(dir, "makemkvcon"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg = &config.Config{MakeMKV: config.MakeMKVConfig{OpticalDrive: "/dev/sr0", InfoTimeout: 5}}
	cmd := newDiscScanCmd()
	got := captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"Scanning disc on /dev/sr0", "Example Disc", "Title 0:", "00800.mpls", "Main Feature"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("min-length", "60"); err != nil {
		t.Fatal(err)
	}
	got = captureStdout(t, func() {
		if err := cmd.RunE(cmd, []string{"disc:1"}); err != nil {
			t.Fatal(err)
		}
	})
	var result struct {
		Device           string `json:"device"`
		MinLengthSeconds int    `json:"min_length_seconds"`
		Titles           []struct {
			ID              int `json:"id"`
			DurationSeconds int `json:"duration_seconds"`
			Segments        int `json:"segments"`
		} `json:"titles"`
	}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid json %q: %v", got, err)
	}
	if result.Device != "disc:1" || result.MinLengthSeconds != 60 || len(result.Titles) != 1 || result.Titles[0].DurationSeconds != 4800 || result.Titles[0].Segments != 2 {
		t.Fatalf("scan: %+v", result)
	}
	cfg.MakeMKV.OpticalDrive = ""
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "no device") {
		t.Fatalf("missing drive: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "makemkvcon"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{"disc:1"}); err == nil || !strings.Contains(err.Error(), "makemkv scan") {
		t.Fatalf("failed scan: %v", err)
	}
}

func TestDiscCommandsWithDaemonResponses(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	cfg = &config.Config{API: config.APIConfig{Token: "secret"}}
	flagSocket = ""
	lock := flock.New(cfg.LockPath())
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	ln, err := net.Listen("unix", cfg.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	response := `{"changed":true,"handled":true,"message":"Disc queued"}`
	status := http.StatusOK
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request: %s %s", r.Method, r.Header.Get("Authorization"))
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	})}
	go func() { _ = server.Serve(ln) }()
	defer func() { _ = server.Close() }()
	for _, tc := range []struct {
		cmd  func() *cobra.Command
		want string
	}{
		{newDiscPauseCmd, "Disc detection paused"},
		{newDiscResumeCmd, "Disc detection resumed"},
		{newDiscDetectCmd, "Disc queued"},
	} {
		c := tc.cmd()
		got := captureStdout(t, func() {
			if err := c.RunE(c, nil); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %q", tc.want, got)
		}
	}
	response = `{"changed":false,"handled":false}`
	for _, tc := range []struct {
		cmd  func() *cobra.Command
		want string
	}{
		{newDiscPauseCmd, "already paused"},
		{newDiscResumeCmd, "already active"},
		{newDiscDetectCmd, "skipped"},
	} {
		c := tc.cmd()
		got := captureStdout(t, func() {
			if err := c.RunE(c, nil); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(got, tc.want) {
			t.Fatalf("%s: %q", tc.want, got)
		}
	}
	if err := daemonDiscPost("/api/disc/pause", nil); err != nil {
		t.Fatalf("discard response: %v", err)
	}
	status, response = http.StatusBadRequest, `{"error":"bad request"}`
	if err := daemonDiscPost("/api/disc/pause", nil); err == nil || err.Error() != "bad request" {
		t.Fatalf("api error: %v", err)
	}
	response = "invalid"
	if err := daemonDiscPost("/api/disc/pause", nil); err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("status error: %v", err)
	}
	status = http.StatusOK
	var result struct {
		Changed bool `json:"changed"`
	}
	if err := daemonDiscPost("/api/disc/pause", &result); err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("decode error: %v", err)
	}
}

func TestDiscCommandsStoppedDaemon(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "missing.sock")
	for _, cmd := range []struct {
		name string
		run  func() error
	}{
		{"pause", func() error { c := newDiscPauseCmd(); return c.RunE(c, nil) }},
		{"resume", func() error { c := newDiscResumeCmd(); return c.RunE(c, nil) }},
	} {
		if err := cmd.run(); err == nil || !strings.Contains(err.Error(), "daemon is not running") {
			t.Errorf("%s: %v", cmd.name, err)
		}
	}
	c := newDiscDetectCmd()
	got := captureStdout(t, func() {
		if err := c.RunE(c, nil); err != nil {
			t.Fatal(err)
		}
	})
	if got != "" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if err := daemonDiscPost("/api/disc/pause", nil); err == nil {
		t.Fatal("posted without daemon")
	}
	disc := newDiscCmd()
	if len(disc.Commands()) != 5 {
		t.Fatalf("subcommands: %d", len(disc.Commands()))
	}
}
