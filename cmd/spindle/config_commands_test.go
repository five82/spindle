package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigInitAndValidate(t *testing.T) {
	env := setupCLITestEnv(t)

	// config validate works even if file exists
	out, _, err := runCLI(t, []string{"config", "validate"}, env.socketPath, "")
	if err != nil {
		t.Fatalf("config validate: %v", err)
	}
	requireContains(t, out, "Configuration valid")

	// config init to temp location
	tmp := t.TempDir()
	target := filepath.Join(tmp, "config.toml")
	out, _, err = runCLI(t, []string{"config", "init", "--path", target}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("config init: %v", err)
	}
	requireContains(t, out, "Wrote sample configuration")

	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected config file at %s: %v", target, err)
	}
}
