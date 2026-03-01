package ripping

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// requiredMakeMKVSettings defines settings that Spindle ensures are configured.
// app_DefaultSelectionString: Select video, audio, and subtitle tracks.
// app_LibdriveIO: Enable libdrive mode for direct disc access, required for UHD and better reliability.
var requiredMakeMKVSettings = map[string]string{
	"app_DefaultSelectionString": "-sel:all,+sel:video,+sel:audio,+sel:subtitle",
	"app_LibdriveIO":             "true",
}

func ensureMakeMKVSettings() error {
	settingsPath, err := resolveMakeMKVSettingsPath()
	if err != nil {
		return err
	}
	return applyMakeMKVSettings(settingsPath, requiredMakeMKVSettings)
}

func resolveMakeMKVSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("resolve home directory: empty path")
	}
	return filepath.Join(home, ".MakeMKV", "settings.conf"), nil
}

func applyMakeMKVSettings(path string, required map[string]string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("settings path is empty")
	}
	if len(required) == 0 {
		return errors.New("no settings to apply")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}

	existing, order, err := readMakeMKVSettings(path)
	if err != nil {
		return err
	}

	// Check if all required settings are already correct
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

	// Apply required settings
	for key, value := range required {
		existing[key] = value
		if !slices.Contains(order, key) {
			order = append(order, key)
		}
	}

	return writeMakeMKVSettings(path, existing, order)
}

func readMakeMKVSettings(path string) (map[string]string, []string, error) {
	settings := make(map[string]string)
	order := make([]string, 0)

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return settings, order, nil
		}
		return nil, nil, fmt.Errorf("open settings: %w", err)
	}
	defer file.Close()

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
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, "\"")
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

func writeMakeMKVSettings(path string, kv map[string]string, order []string) error {
	keys := make([]string, 0, len(kv))
	keys = append(keys, order...)
	for key := range kv {
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	// ensure deterministic order when file previously empty
	dedup := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dedup = append(dedup, key)
	}
	if len(order) == 0 {
		sort.Strings(dedup)
	}

	builder := &strings.Builder{}
	builder.WriteString("# MakeMKV settings file (managed by Spindle)\n")
	for _, key := range dedup {
		value := kv[key]
		if _, err := fmt.Fprintf(builder, "%s = \"%s\"\n", key, escapeQuotes(value)); err != nil {
			return fmt.Errorf("write selection rule: %w", err)
		}
	}

	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

func escapeQuotes(value string) string {
	return strings.ReplaceAll(value, "\"", "\\\"")
}
