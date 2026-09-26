package main

import (
	"os"
	"strings"
	"testing"
)

func TestMainHelpRegistersOperatorCommands(t *testing.T) {
	oldArgs := os.Args
	os.Args = []string{"spindle", "--help"}
	t.Cleanup(func() { os.Args = oldArgs })
	output := captureStdout(t, main)
	for _, command := range []string{"start", "status", "queue", "cache", "disc", "staging", "debug"} {
		if !strings.Contains(output, command) {
			t.Errorf("root help missing %q", command)
		}
	}
}

func TestBuildVersionHasVersion(t *testing.T) {
	if got := buildVersion(); got == "" {
		t.Fatal("empty version")
	}
}
