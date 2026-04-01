package makemkv

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/five82/spindle/internal/logs"
)

// DiscInfo represents the results of a MakeMKV disc scan.
type DiscInfo struct {
	Name     string
	Titles   []TitleInfo
	RawLines []string
}

// TitleInfo represents a single title on a disc.
type TitleInfo struct {
	ID           int
	Name         string
	Duration     time.Duration
	Chapters     int
	SizeBytes    int64
	SegmentCount int
	SegmentMap   string
	Playlist     string
}

// RipProgress reports ripping progress.
type RipProgress struct {
	TitleID int
	Current int
	Total   int
	Percent float64
	Message string
}

// Scan runs makemkvcon info on the given device and parses disc information.
// The device string is normalized: empty defaults to "disc:0", paths starting
// with /dev/ become "dev:<path>", and already-prefixed values pass through.
func Scan(ctx context.Context, device string, timeout time.Duration, minLength int, logger *slog.Logger) (*DiscInfo, error) {
	logger = logs.Default(logger)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	src := normalizeDevice(device)
	minLenFlag := fmt.Sprintf("--minlength=%d", minLength)

	logger.Info("MakeMKV scan started",
		"event_type", "makemkv_scan_start",
		"device", src,
	)

	cmd := exec.CommandContext(ctx, "makemkvcon", "--robot", "--progress=-same", "info", src, minLenFlag)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Error("MakeMKV scan stdout pipe failed",
			"event_type", "makemkv_scan_error",
			"error_hint", "failed to create stdout pipe for makemkvcon",
			"error", err,
		)
		return nil, fmt.Errorf("makemkv scan: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		logger.Error("MakeMKV scan start failed",
			"event_type", "makemkv_scan_error",
			"error_hint", "failed to start makemkvcon process",
			"error", err,
		)
		return nil, fmt.Errorf("makemkv scan: start: %w", err)
	}

	var lines []string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		logger.Error("MakeMKV scan read failed",
			"event_type", "makemkv_scan_error",
			"error_hint", "failed to read makemkvcon output",
			"error", err,
		)
		return nil, fmt.Errorf("makemkv scan: read output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		logger.Error("MakeMKV scan failed",
			"event_type", "makemkv_scan_error",
			"error_hint", "makemkvcon exited with error",
			"error", err,
		)
		return nil, fmt.Errorf("makemkv scan: %w", err)
	}

	info := parseRobotOutput(lines)
	logger.Info("MakeMKV scan completed",
		"event_type", "makemkv_scan_complete",
		"device", src,
		"titles_found", len(info.Titles),
		"disc_name", info.Name,
	)
	return info, nil
}

// Rip runs makemkvcon mkv to rip a single title from disc to outputDir.
// The progress callback, if non-nil, is called with progress updates.
func Rip(ctx context.Context, device string, titleID int, outputDir string, timeout time.Duration, minLength int, progress func(RipProgress), logger *slog.Logger) error {
	logger = logs.Default(logger)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	src := normalizeDevice(device)
	titleStr := strconv.Itoa(titleID)
	minLenFlag := fmt.Sprintf("--minlength=%d", minLength)

	logger.Info("MakeMKV rip started",
		"event_type", "makemkv_rip_start",
		"device", src,
		"title_id", titleID,
		"output_dir", outputDir,
	)

	cmd := exec.CommandContext(ctx, "makemkvcon", "--robot", "--progress=-same", "mkv", src, titleStr, outputDir, minLenFlag)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Error("MakeMKV rip stdout pipe failed",
			"event_type", "makemkv_rip_error",
			"error_hint", "failed to create stdout pipe for makemkvcon",
			"error", err,
		)
		return fmt.Errorf("makemkv rip: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		logger.Error("MakeMKV rip start failed",
			"event_type", "makemkv_rip_error",
			"error_hint", "failed to start makemkvcon rip process",
			"error", err,
		)
		return fmt.Errorf("makemkv rip: start: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if progress != nil {
			if p, ok := parsePRGV(line, titleID); ok {
				progress(p)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		logger.Error("MakeMKV rip read failed",
			"event_type", "makemkv_rip_error",
			"error_hint", "failed to read makemkvcon rip output",
			"error", err,
		)
		return fmt.Errorf("makemkv rip: read output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		logger.Error("MakeMKV rip failed",
			"event_type", "makemkv_rip_error",
			"error_hint", "makemkvcon rip exited with error",
			"error", err,
			"title_id", titleID,
		)
		return fmt.Errorf("makemkv rip: %w", err)
	}

	logger.Info("MakeMKV rip completed",
		"event_type", "makemkv_rip_complete",
		"device", src,
		"title_id", titleID,
	)
	return nil
}

// normalizeDevice converts a device string to the format expected by makemkvcon.
func normalizeDevice(device string) string {
	switch {
	case device == "":
		return "disc:0"
	case strings.HasPrefix(device, "/dev/"):
		return "dev:" + device
	default:
		return device
	}
}

// parseRobotOutput parses makemkvcon robot-format output lines into a DiscInfo.
func parseRobotOutput(lines []string) *DiscInfo {
	info := &DiscInfo{
		RawLines: lines,
	}

	// Collect title attributes keyed by title ID.
	titles := make(map[int]*titleAttrs)

	for _, line := range lines {
		prefix, body, ok := splitRobotLine(line)
		if !ok {
			continue
		}

		switch prefix {
		case "CINFO":
			parseCINFO(body, info)
		case "TINFO":
			parseTINFO(body, titles)
		}
	}

	// Convert map to sorted slice.
	if len(titles) > 0 {
		maxID := 0
		for id := range titles {
			if id > maxID {
				maxID = id
			}
		}
		for id := 0; id <= maxID; id++ {
			ta, ok := titles[id]
			if !ok {
				continue
			}
			info.Titles = append(info.Titles, TitleInfo{
				ID:           id,
				Name:         ta.name,
				Duration:     ta.duration,
				Chapters:     ta.chapters,
				SizeBytes:    ta.sizeBytes,
				SegmentCount: ta.segmentCount,
				SegmentMap:   ta.segmentMap,
				Playlist:     ta.playlist,
			})
		}
	}

	return info
}

// splitRobotLine splits "PREFIX:body" and returns (prefix, body, ok).
func splitRobotLine(line string) (string, string, bool) {
	idx := strings.IndexByte(line, ':')
	if idx < 1 {
		return "", "", false
	}
	return line[:idx], line[idx+1:], true
}

// parseCINFO handles CINFO lines: attrID,attrType,value
func parseCINFO(body string, info *DiscInfo) {
	fields := splitFields(body, 3)
	if len(fields) < 3 {
		return
	}
	attrID, err := strconv.Atoi(fields[0])
	if err != nil {
		return
	}
	value := unquote(fields[2])
	if attrID == 2 {
		info.Name = value
	}
}

// parseTINFO handles TINFO lines: titleID,attrID,attrType,value
func parseTINFO(body string, titles map[int]*titleAttrs) {
	fields := splitFields(body, 4)
	if len(fields) < 4 {
		return
	}
	titleID, err := strconv.Atoi(fields[0])
	if err != nil {
		return
	}
	attrID, err := strconv.Atoi(fields[1])
	if err != nil {
		return
	}
	value := unquote(fields[3])

	ta := titles[titleID]
	if ta == nil {
		ta = &titleAttrs{}
		titles[titleID] = ta
	}

	switch attrID {
	case 2:
		ta.name = value
	case 8:
		ta.chapters, _ = strconv.Atoi(value)
	case 9:
		ta.duration = parseDuration(value)
	case 10:
		ta.sizeBytes, _ = strconv.ParseInt(value, 10, 64)
	case 16:
		ta.playlist = value
	case 25:
		ta.segmentCount, _ = strconv.Atoi(value)
	case 26:
		ta.segmentMap = value
	}
}

// titleAttrs accumulates raw title attributes during parsing.
type titleAttrs struct {
	name         string
	duration     time.Duration
	chapters     int
	sizeBytes    int64
	segmentCount int
	segmentMap   string
	playlist     string
}

// parsePRGV parses a PRGV progress line and returns a RipProgress.
// Format: PRGV:current,total,max
func parsePRGV(line string, titleID int) (RipProgress, bool) {
	prefix, body, ok := splitRobotLine(line)
	if !ok || prefix != "PRGV" {
		return RipProgress{}, false
	}
	fields := splitFields(body, 3)
	if len(fields) < 3 {
		return RipProgress{}, false
	}
	current, err := strconv.Atoi(fields[0])
	if err != nil {
		return RipProgress{}, false
	}
	total, err := strconv.Atoi(fields[1])
	if err != nil {
		return RipProgress{}, false
	}
	max, err := strconv.Atoi(fields[2])
	if err != nil {
		return RipProgress{}, false
	}

	var pct float64
	if max > 0 {
		pct = float64(current) / float64(max) * 100
	}

	return RipProgress{
		TitleID: titleID,
		Current: current,
		Total:   total,
		Percent: pct,
	}, true
}

// parseDuration parses a duration string in "H:MM:SS" format.
func parseDuration(s string) time.Duration {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	sec, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second
}

// splitFields splits a comma-separated string into at most n fields.
// Handles quoted values containing commas.
func splitFields(s string, n int) []string {
	var fields []string
	var current strings.Builder
	inQuote := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '"':
			inQuote = !inQuote
			current.WriteByte(ch)
		case ch == ',' && !inQuote:
			fields = append(fields, current.String())
			current.Reset()
			if len(fields) == n-1 {
				// Last field gets the remainder.
				fields = append(fields, s[i+1:])
				return fields
			}
		default:
			current.WriteByte(ch)
		}
	}
	fields = append(fields, current.String())
	return fields
}

// unquote removes surrounding double quotes from a string.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// HasForcedEnglishSubtitles returns true if any title has a forced English
// subtitle track. MakeMKV marks forced tracks with "(forced only)" in the
// track name (SINFO attribute 30).
// SINFO attribute IDs used by MakeMKV robot output.
const (
	sinfoAttrTrackType = 1  // "Video", "Audio", "Subtitle"
	sinfoAttrLanguage  = 3  // e.g. "eng"
	sinfoAttrTrackName = 30 // e.g. "PGS English (forced only)"
)

func (d *DiscInfo) HasForcedEnglishSubtitles() bool {
	if d == nil {
		return false
	}

	type streamKey struct{ title, stream int }
	type streamAttrs struct {
		trackType string
		language  string
		name      string
	}

	streams := make(map[streamKey]*streamAttrs)

	for _, line := range d.RawLines {
		prefix, body, ok := splitRobotLine(line)
		if !ok || prefix != "SINFO" {
			continue
		}
		fields := splitFields(body, 5)
		if len(fields) < 5 {
			continue
		}
		titleID, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		streamID, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		attrID, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		value := unquote(fields[4])

		key := streamKey{titleID, streamID}
		sa := streams[key]
		if sa == nil {
			sa = &streamAttrs{}
			streams[key] = sa
		}

		switch attrID {
		case sinfoAttrTrackType:
			sa.trackType = value
		case sinfoAttrLanguage:
			sa.language = value
		case sinfoAttrTrackName:
			sa.name = value
			// Short-circuit: check match as soon as we have the track name.
			if strings.EqualFold(sa.trackType, "Subtitle") &&
				strings.HasPrefix(strings.ToLower(sa.language), "eng") &&
				strings.Contains(strings.ToLower(value), "(forced only)") {
				return true
			}
		}
	}
	return false
}
