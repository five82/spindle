package disc

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type makeMKVParser struct{}

func (makeMKVParser) Parse(data []byte) (*ScanResult, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, errors.New("makemkv produced empty output")
	}

	lines := strings.Split(text, "\n")
	fingerprint := extractFingerprint(lines)
	titles := extractTitles(lines)

	return &ScanResult{Fingerprint: fingerprint, Titles: titles}, nil
}

var fingerprintPattern = regexp.MustCompile(`[0-9A-Fa-f]{16,}`)

func extractFingerprint(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(strings.ToLower(trimmed), "fingerprint") {
			match := fingerprintPattern.FindString(trimmed)
			if match != "" {
				return strings.ToUpper(match)
			}
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "CINFO:") {
			continue
		}
		payload := strings.TrimPrefix(trimmed, "CINFO:")
		parts := strings.SplitN(payload, ",", 3)
		if len(parts) < 3 {
			continue
		}
		if strings.TrimSpace(parts[0]) != "32" {
			continue
		}
		value := strings.TrimSpace(parts[2])
		value = strings.Trim(value, "\"")
		match := fingerprintPattern.FindString(value)
		if match != "" {
			return strings.ToUpper(match)
		}
	}

	match := fingerprintPattern.FindString(strings.Join(lines, "\n"))
	if match != "" {
		return strings.ToUpper(match)
	}
	return ""
}

func extractTitles(lines []string) []Title {
	type titleData struct {
		id       int
		name     string
		duration int
	}

	results := make(map[int]*titleData)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "TINFO:") {
			continue
		}
		payload := strings.TrimPrefix(trimmed, "TINFO:")
		parts := strings.SplitN(payload, ",", 4)
		if len(parts) < 4 {
			continue
		}
		titleID, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		attrID, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			continue
		}
		value := strings.TrimSpace(parts[3])
		value = strings.Trim(value, "\"")
		entry, ok := results[titleID]
		if !ok {
			entry = &titleData{id: titleID}
			results[titleID] = entry
		}
		switch attrID {
		case 2:
			if value != "" {
				entry.name = value
			}
		case 9:
			entry.duration = parseDuration(value)
		}
	}

	ids := make([]int, 0, len(results))
	for id := range results {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	titles := make([]Title, 0, len(ids))
	for _, id := range ids {
		entry := results[id]
		titles = append(titles, Title{ID: entry.id, Name: entry.name, Duration: entry.duration})
	}
	return titles
}

func parseDuration(value string) int {
	clean := value
	if strings.Contains(clean, ",\"") {
		parts := strings.SplitN(clean, ",\"", 2)
		clean = parts[1]
	}
	clean = strings.Trim(clean, "\"")
	if clean == "" {
		return 0
	}
	segments := strings.Split(clean, ":")
	if len(segments) != 3 {
		return 0
	}
	hours, err := strconv.Atoi(segments[0])
	if err != nil {
		return 0
	}
	minutes, err := strconv.Atoi(segments[1])
	if err != nil {
		return 0
	}
	seconds, err := strconv.Atoi(segments[2])
	if err != nil {
		return 0
	}
	return hours*3600 + minutes*60 + seconds
}
