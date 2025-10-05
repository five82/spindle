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
	builders := make(map[int]*titleBuilder)

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "TINFO:"):
			parseTInfo(builders, line)
		case strings.HasPrefix(line, "SINFO:"):
			parseSInfo(builders, line)
		}
	}

	ids := make([]int, 0, len(builders))
	for id := range builders {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	titles := make([]Title, 0, len(ids))
	for _, id := range ids {
		builder := builders[id]
		tracks := make([]Track, 0, len(builder.order))
		for _, streamID := range builder.order {
			track := builder.tracks[streamID]
			if track == nil {
				continue
			}
			copy := *track
			if len(copy.Attributes) == 0 {
				copy.Attributes = nil
			}
			tracks = append(tracks, copy)
		}
		titles = append(titles, Title{ID: builder.id, Name: builder.name, Duration: builder.duration, Tracks: tracks})
	}

	return titles
}

func parseTInfo(results map[int]*titleBuilder, line string) {
	payload := strings.TrimPrefix(line, "TINFO:")
	parts := strings.SplitN(payload, ",", 4)
	if len(parts) < 4 {
		return
	}
	titleID, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return
	}
	attrID, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return
	}
	value := strings.TrimSpace(parts[3])
	value = strings.Trim(value, "\"")
	entry := ensureTitleBuilder(results, titleID)
	switch attrID {
	case 2:
		if value != "" {
			entry.name = value
		}
	case 9:
		entry.duration = parseDuration(value)
	}
}

func parseSInfo(results map[int]*titleBuilder, line string) {
	payload := strings.TrimPrefix(line, "SINFO:")
	parts := strings.SplitN(payload, ",", 5)
	if len(parts) < 5 {
		return
	}
	titleID, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return
	}
	streamID, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return
	}
	attrID, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil {
		return
	}
	value := strings.TrimSpace(parts[4])
	value = strings.Trim(value, "\"")
	entry := ensureTitleBuilder(results, titleID)
	track := entry.ensureTrack(streamID)
	if track.Attributes == nil {
		track.Attributes = make(map[int]string)
	}
	if value != "" {
		track.Attributes[attrID] = value
	}

	switch attrID {
	case 1:
		track.Type = classifyTrackType(value)
	case 2:
		if track.Name == "" {
			track.Name = value
		}
	case 3, 28:
		// ISO-639-2 language code (primary or alternative field depending on MakeMKV version).
		if track.Language == "" {
			track.Language = strings.ToLower(value)
		}
	case 4, 29:
		if track.LanguageName == "" {
			track.LanguageName = value
		}
	case 5:
		track.CodecID = value
	case 6:
		track.CodecShort = value
	case 7:
		track.CodecLong = value
	case 13:
		track.BitRate = value
	case 14:
		if ch, err := strconv.Atoi(value); err == nil && ch > 0 {
			track.ChannelCount = ch
		}
	case 30:
		track.Name = value
	case 40:
		track.ChannelLayout = value
	}
}

func classifyTrackType(value string) TrackType {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(lower, "video"):
		return TrackTypeVideo
	case strings.Contains(lower, "audio"):
		return TrackTypeAudio
	case strings.Contains(lower, "sub") || strings.Contains(lower, "text"):
		return TrackTypeSubtitle
	case strings.Contains(lower, "data"):
		return TrackTypeData
	default:
		return TrackTypeUnknown
	}
}

type titleBuilder struct {
	id       int
	name     string
	duration int
	tracks   map[int]*Track
	order    []int
}

func ensureTitleBuilder(results map[int]*titleBuilder, id int) *titleBuilder {
	if existing, ok := results[id]; ok {
		return existing
	}
	builder := &titleBuilder{
		id:     id,
		tracks: make(map[int]*Track),
	}
	results[id] = builder
	return builder
}

func (b *titleBuilder) ensureTrack(streamID int) *Track {
	if track, ok := b.tracks[streamID]; ok {
		return track
	}
	track := &Track{StreamID: streamID, Type: TrackTypeUnknown}
	track.Order = len(b.order)
	b.tracks[streamID] = track
	b.order = append(b.order, streamID)
	return track
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
