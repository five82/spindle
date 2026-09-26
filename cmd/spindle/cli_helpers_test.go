package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestParseQueueIDs(t *testing.T) {
	ids, err := parseQueueIDs([]string{"1", "200", "-3"})
	if err != nil || !reflect.DeepEqual(ids, []int64{1, 200, -3}) {
		t.Fatalf("parseQueueIDs = %v, %v", ids, err)
	}
	if _, err := parseQueueIDs([]string{"1", "not-an-id"}); err == nil || !strings.Contains(err.Error(), "invalid item ID") {
		t.Fatalf("parseQueueIDs invalid = %v", err)
	}
	if _, err := parseQueueID("9999999999999999999999"); err == nil {
		t.Fatal("overflowing ID accepted")
	}
}

func TestOutputAndJSONHelpers(t *testing.T) {
	if commandOutput(false) != os.Stdout {
		t.Fatal("normal command output does not use stdout")
	}
	var buf bytes.Buffer
	printCommandOutput(&buf, "item %d", 7)
	if buf.String() != "item 7" {
		t.Errorf("printCommandOutput = %q", buf.String())
	}
	if n, err := commandOutput(true).Write([]byte("quiet")); n != 5 || err != nil {
		t.Errorf("quiet write = %d, %v", n, err)
	}
	if got := prettyJSON("invalid{"); got != "invalid{" {
		t.Errorf("invalid JSON = %q", got)
	}
	got := prettyJSON(`{"a":1}`)
	var decoded map[string]int
	if err := json.Unmarshal([]byte(got), &decoded); err != nil || decoded["a"] != 1 || !strings.Contains(got, "\n") {
		t.Errorf("prettyJSON = %q, %v", got, err)
	}
	if shortFP("abcdefghijklmnop") != "abcdefghijkl" || shortFP("short") != "short" {
		t.Fatal("shortFP did not preserve short IDs or truncate long ones")
	}
	if err := printJSON(make(chan int)); err == nil {
		t.Fatal("printJSON accepted an unsupported type")
	}
	out := captureStdout(t, func() {
		if err := printJSON(map[string]int{"count": 2}); err != nil {
			t.Errorf("printJSON: %v", err)
		}
	})
	if out != "{\n  \"count\": 2\n}\n" {
		t.Errorf("printJSON output = %q", out)
	}
}

func TestResolveTargetDirectPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.mkv")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveTarget(path)
	if err != nil || got != path {
		t.Fatalf("resolveTarget = %q, %v", got, err)
	}
	for _, missing := range []string{filepath.Join(t.TempDir(), "missing"), "0"} {
		if _, err := resolveTarget(missing); err == nil || !strings.Contains(err.Error(), "file not found") {
			t.Errorf("resolveTarget(%q) = %v", missing, err)
		}
	}
}

func TestPathsAndConfirmation(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	cfg, flagSocket = nil, ""
	if socketPath() != "" || lockPath() != "" {
		t.Fatal("paths without config should be empty")
	}
	cfg = &config.Config{}
	if socketPath() != cfg.SocketPath() || lockPath() != cfg.LockPath() {
		t.Fatal("configured paths not used")
	}
	flagSocket = "/tmp/custom.sock"
	if socketPath() != flagSocket {
		t.Fatal("socket override not used")
	}
	if err := confirm("delete", true); err != nil {
		t.Fatalf("--yes confirmation: %v", err)
	}
	if !stdinIsTTY() {
		if err := confirm("delete", false); err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Errorf("non-interactive confirmation: %v", err)
		}
	}
}
