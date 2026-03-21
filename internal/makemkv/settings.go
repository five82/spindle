package makemkv

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// requiredSettings defines settings that Spindle ensures are configured.
// app_DefaultSelectionString: Select video, audio, and subtitle tracks.
// app_LibdriveIO: Enable libdrive mode for direct disc access (required for UHD).
var requiredSettings = map[string]string{
	"app_DefaultSelectionString": "-sel:all,+sel:video,+sel:audio,+sel:subtitle",
	"app_LibdriveIO":             "true",
}

// EnsureSettings verifies that required MakeMKV settings are configured
// in ~/.MakeMKV/settings.conf. Only writes the file if settings need updating.
func EnsureSettings(logger *slog.Logger) error {
	settingsPath, err := resolveSettingsPath()
	if err != nil {
		return err
	}
	return applySettings(settingsPath, requiredSettings, logger)
}

func resolveSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("resolve home directory: empty path")
	}
	return filepath.Join(home, ".MakeMKV", "settings.conf"), nil
}

func applySettings(path string, required map[string]string, logger *slog.Logger) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}

	existing, order, err := readSettings(path)
	if err != nil {
		return err
	}

	// Check if all required settings are already correct.
	needsUpdate := false
	for key, value := range required {
		if existing[key] != value {
			needsUpdate = true
			break
		}
	}
	if !needsUpdate {
		return nil
	}

	// Apply required settings.
	for key, value := range required {
		existing[key] = value
		if !slices.Contains(order, key) {
			order = append(order, key)
		}
	}

	logger.Info("updating MakeMKV settings",
		"decision_type", "makemkv_settings",
		"decision_result", "updated",
		"decision_reason", "required settings not configured",
		"path", path,
	)
	return writeSettings(path, existing, order)
}

func readSettings(path string) (map[string]string, []string, error) {
	settings := make(map[string]string)
	var order []string

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return settings, order, nil
		}
		return nil, nil, fmt.Errorf("open settings: %w", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), "\"")
		settings[key] = value
		if !slices.Contains(order, key) {
			order = append(order, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("read settings: %w", err)
	}
	return settings, order, nil
}

func writeSettings(path string, kv map[string]string, order []string) error {
	// Build deduplicated key list preserving original order.
	seen := make(map[string]struct{}, len(order))
	var keys []string
	for _, key := range order {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range kv {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	// Sort when no original order existed.
	if len(order) == 0 {
		sort.Strings(keys)
	}

	var b strings.Builder
	for _, key := range keys {
		value := strings.ReplaceAll(kv[key], "\"", "\\\"")
		fmt.Fprintf(&b, "%s = \"%s\"\n", key, value)
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}
