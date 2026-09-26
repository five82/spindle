package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestFlagOrEnv(t *testing.T) {
	t.Setenv("FLYER_API_TOKEN", "environment-token")
	if got := flagOrEnv("flag-token", "FLYER_API_TOKEN"); got != "flag-token" {
		t.Fatalf("flagOrEnv(flag) = %q", got)
	}
	if got := flagOrEnv("", "FLYER_API_TOKEN"); got != "environment-token" {
		t.Fatalf("flagOrEnv(env) = %q", got)
	}
	t.Setenv("FLYER_API_TOKEN", "")
	if got := flagOrEnv("", "FLYER_API_TOKEN"); got != "" {
		t.Fatalf("flagOrEnv(empty) = %q", got)
	}
}

func TestRunReportsConfigError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "invalid.toml")
	if err := os.WriteFile(path, []byte("[api\nbind = 'invalid'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	oldArgs, oldFlags := os.Args, flag.CommandLine
	t.Cleanup(func() { os.Args, flag.CommandLine = oldArgs, oldFlags })
	os.Args = []string{"flyer", "-config", path, "-poll", "3", "-api", "http://localhost:7487", "-token", "test-token"}
	flag.CommandLine = flag.NewFlagSet("flyer", flag.ContinueOnError)
	if got := run(); got != 1 {
		t.Fatalf("run() = %d, want 1 for malformed config", got)
	}
}
