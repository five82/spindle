package disc

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ReadLabel returns the first non-empty disc label from lsblk output.
func ReadLabel(ctx context.Context, device string, timeout time.Duration) (string, error) {
	device = strings.TrimSpace(device)
	if device == "" {
		return "", fmt.Errorf("no device specified")
	}

	lsblkCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		lsblkCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	output, err := exec.CommandContext(lsblkCtx, "lsblk", "-P", "-o", "LABEL,FSTYPE", device).Output()
	if err != nil {
		return "", fmt.Errorf("failed to run lsblk: %w", err)
	}

	label, fstype := ParseLSBLKLabelFSType(string(output))
	if strings.TrimSpace(label) != "" && strings.TrimSpace(fstype) != "" {
		return label, nil
	}
	return "", fmt.Errorf("no disc label found")
}

// ParseLSBLKLabelFSType parses lsblk -P output and returns the first LABEL/FSTYPE pair.
func ParseLSBLKLabelFSType(output string) (string, string) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		data := parseLSBLKKeyValueLine(line)
		if len(data) == 0 {
			continue
		}
		return data["LABEL"], data["FSTYPE"]
	}
	return "", ""
}

func parseLSBLKKeyValueLine(line string) map[string]string {
	result := make(map[string]string)
	fields := strings.Fields(line)
	for _, field := range fields {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, "\"")
		result[key] = value
	}
	return result
}
