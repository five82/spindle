package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Config contains the Spindle settings Flyer needs for API discovery and
// diagnostics.
type Config struct {
	APIBind  string
	APIToken string
	StateDir string
}

const defaultStateDir = "~/.local/state/spindle"

// Load reads Spindle's current [api] and [paths] sections. A missing file is
// allowed so explicit --api/--token remote access does not require local
// Spindle configuration.
func Load(path string) (Config, error) {
	resolved, err := resolvePath(path)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{StateDir: mustExpand(defaultStateDir)}
	file, err := os.Open(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var raw struct {
		API struct {
			Bind  string `toml:"bind"`
			Token string `toml:"token"`
		} `toml:"api"`
		Paths struct {
			StateDir string `toml:"state_dir"`
		} `toml:"paths"`
	}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	cfg.APIBind = strings.TrimSpace(raw.API.Bind)
	cfg.APIToken = strings.TrimSpace(raw.API.Token)
	if stateDir := strings.TrimSpace(raw.Paths.StateDir); stateDir != "" {
		cfg.StateDir = mustExpand(stateDir)
	}
	return cfg, nil
}

// DaemonLogPath returns Spindle's active daemon-log link.
func (c Config) DaemonLogPath() string {
	stateDir := strings.TrimSpace(c.StateDir)
	if stateDir == "" {
		stateDir = mustExpand(defaultStateDir)
	}
	return filepath.Join(stateDir, "daemon.log")
}

func resolvePath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		return expandPath(path)
	}
	configDir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configDir == "" {
		var err error
		configDir, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve user config directory: %w", err)
		}
	} else if !filepath.IsAbs(configDir) {
		return "", fmt.Errorf("resolve user config directory: XDG_CONFIG_HOME is not an absolute path")
	}
	return filepath.Join(configDir, "spindle", "config.toml"), nil
}

func mustExpand(path string) string {
	expanded, err := expandPath(path)
	if err != nil {
		return path
	}
	return expanded
}

func expandPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("path is empty")
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		trimmed = filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
	}
	return filepath.Abs(trimmed)
}
