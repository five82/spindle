// Package prefs handles Flyer user preferences persistence.
// Preferences are stored in ~/.config/flyer/prefs.toml.
package prefs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Prefs holds user preferences for Flyer.
type Prefs struct {
	Theme string `toml:"theme"`
}

const (
	defaultPrefsPath = "~/.config/flyer/prefs.toml"
	defaultTheme     = "Slate"
)

// DefaultPath returns the default preferences file path.
func DefaultPath() string {
	return defaultPrefsPath
}

// Load reads preferences from the given path, falling back to defaults if missing or invalid.
func Load(path string) Prefs {
	prefs := Prefs{Theme: defaultTheme}

	resolved, err := resolvePath(path)
	if err != nil {
		return prefs
	}

	bytes, err := os.ReadFile(resolved)
	if err != nil {
		return prefs
	}

	if err := toml.Unmarshal(bytes, &prefs); err != nil {
		return Prefs{Theme: defaultTheme}
	}

	if strings.TrimSpace(prefs.Theme) == "" {
		prefs.Theme = defaultTheme
	}

	return prefs
}

// Save writes preferences to the given path, creating directories as needed.
func Save(path string, p Prefs) error {
	resolved, err := resolvePath(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create prefs dir: %w", err)
	}

	bytes, err := toml.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal prefs: %w", err)
	}

	if err := os.WriteFile(resolved, bytes, 0o644); err != nil {
		return fmt.Errorf("write prefs: %w", err)
	}

	return nil
}

func resolvePath(path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		p = defaultPrefsPath
	}
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Abs(p)
}
