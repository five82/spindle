//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployScript(t *testing.T) {
	script, err := filepath.Abs("../../deploy.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, tool, running, calls, build string
		first, failBuild, failStart       bool
	}{
		{name: "running daemon", tool: "spindle", running: "true", calls: "status --json\nstop\nstart\nstatus --json\n", build: "1 ./cmd/spindle\n"},
		{name: "stopped daemon", tool: "spindle", running: "false", calls: "status --json\nstatus --json\n", build: "1 ./cmd/spindle\n"},
		{name: "first daemon install", tool: "spindle", running: "false", first: true, calls: "status --json\n", build: "1 ./cmd/spindle\n"},
		{name: "flyer", tool: "flyer", build: "0 ./flyer/cmd/flyer\n"},
		{name: "reel", tool: "reel", build: "1 ./reel/cmd/reel\n"},
		{name: "build failure leaves daemon alone", tool: "spindle", running: "true", failBuild: true, build: "1 ./cmd/spindle\n"},
		{name: "startup failure leaves daemon stopped", tool: "spindle", running: "true", failStart: true, calls: "status --json\nstop\nstart\nstop\n", build: "1 ./cmd/spindle\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(path, content string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(content), 0700); err != nil {
					t.Fatal(err)
				}
			}
			read := func(path string) string {
				t.Helper()
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			// Whitelist utilities rather than inheriting any directory that might
			// contain the operator's real daemon, including /usr/bin.
			for _, name := range []string{"awk", "chmod", "cmp", "cp", "dirname", "mkdir", "mktemp", "mv", "rm"} {
				path, err := exec.LookPath(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path, filepath.Join(bin, name)); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			t.Setenv("HOME", dir)
			t.Setenv("TEST_ROOT", dir)
			t.Setenv("FAIL_BUILD", "false")
			t.Setenv("FAIL_START", "false")
			if tc.failBuild {
				t.Setenv("FAIL_BUILD", "true")
			}
			if tc.failStart {
				t.Setenv("FAIL_START", "true")
			}
			write(filepath.Join(dir, "state"), tc.running)
			write(filepath.Join(dir, "calls"), "")
			write(filepath.Join(bin, "go"), `#!/bin/bash
set -euo pipefail
if [ "$1" = env ]; then echo "$TEST_ROOT"; exit 0; fi
printf '%s %s\n' "$CGO_ENABLED" "${@: -1}" >> "$TEST_ROOT/build"
if [ "$FAIL_BUILD" = true ]; then exit 19; fi
while [ "$1" != -o ]; do shift; done
cp "$TEST_ROOT/candidate" "$2"
chmod 700 "$2"
`)
			binary := `#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> "$TEST_ROOT/calls"
case "$1" in
    status) printf '{\n  "running": %s\n}\n' "$(<"$TEST_ROOT/state")" ;;
    stop) printf false > "$TEST_ROOT/state" ;;
    start)
        if [ "$FAIL_START" = true ]; then exit 20; fi
        printf true > "$TEST_ROOT/state" ;;
    *) exit 21 ;;
esac
`
			candidate := binary + "# candidate\n"
			previous := binary + "# previous\n"
			write(filepath.Join(dir, "candidate"), candidate)
			target := filepath.Join(bin, tc.tool)
			if !tc.first {
				write(target, previous)
			}
			out, err := exec.Command("/bin/bash", script, tc.tool).CombinedOutput()
			if (err != nil) != (tc.failBuild || tc.failStart) {
				t.Fatalf("deploy: %v\n%s", err, out)
			}
			if got := read(filepath.Join(dir, "build")); got != tc.build {
				t.Errorf("build = %q, want %q", got, tc.build)
			}
			if got := read(filepath.Join(dir, "calls")); got != tc.calls {
				t.Errorf("daemon calls = %q, want %q", got, tc.calls)
			}
			wantBinary := candidate
			if tc.failBuild {
				wantBinary = previous
			}
			if got := read(target); got != wantBinary {
				t.Error("installed binary does not match expected version")
			}
			if !tc.first && !tc.failBuild {
				if got := read(target + ".previous"); got != previous {
					t.Error("previous binary was not preserved")
				}
			} else if _, err := os.Stat(target + ".previous"); !os.IsNotExist(err) {
				t.Errorf("unexpected previous binary: %v", err)
			}
			wantState := tc.running
			if tc.failStart {
				wantState = "false"
			}
			if got := read(filepath.Join(dir, "state")); got != wantState {
				t.Errorf("daemon state = %q, want %q", got, wantState)
			}
		})
	}
}

func TestDeployScriptRequiresExplicitTarget(t *testing.T) {
	script, err := filepath.Abs("../../deploy.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"all"}, {"flyer", "reel"}} {
		out, err := exec.Command("/bin/bash", append([]string{script}, args...)...).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "Usage:") {
			t.Fatalf("args %q: %v\n%s", args, err, out)
		}
	}
}
