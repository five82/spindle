package main

import (
	"os"
	"testing"
)

func TestShowLines(t *testing.T) {
	env := setupCLITestEnv(t)

	logPath := env.logPath
	if err := os.WriteFile(logPath, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	out, _, err := runCLI(t, []string{"show", "--lines", "2"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("show --lines: %v", err)
	}
	requireContains(t, out, "second")
	requireContains(t, out, "third")
}
