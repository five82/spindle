package logging

import (
	"log/slog"
	"strings"
)

type infoField struct {
	label string
	value string
}

const infoAttrLimit = 8

var infoHighlightKeys = []string{
	FieldAlert,
	FieldEventType,
	FieldDecisionType,
	"disc_title",
	"disc_label",
	"processing_status",
	FieldProgressStage,
	FieldProgressPercent,
	FieldProgressMessage,
	FieldProgressETA,
	"command",
	"error_message",
	FieldErrorCode,
	FieldErrorHint,
	FieldErrorDetailPath,
	"status",
	"disc_type",
	"runtime_minutes",
	"studio",
	"hardware_hostname",
	"video_file",
	"video_output",
	"video_duration",
	"video_resolution",
	"video_dynamic_range",
	"video_audio",
	"crop_message",
	"crop_status",
	"encoding_encoder",
	"encoding_preset",
	"encoding_tune",
	"encoding_quality",
	"encoding_pixel_format",
	"encoding_audio_codec",
	"encoding_audio",
	"encoding_drapto_preset",
	"encoding_preset_values",
	"encoding_svt_params",
	"validation_status",
	"validation_step",
	"validation_details",
	"encoding_result_input",
	"encoding_result_output",
	"encoding_result_size",
	"encoding_result_reduction",
	"encoding_result_video",
	"encoding_result_audio",
	"encoding_result_duration",
	"encoding_result_location",
	"drapto_warning",
	"drapto_error_title",
	"drapto_error_message",
	"preset_reason",
	"decision_result",
	"decision_selected",
	"decision_candidates",
	"decision_rejects",
	// Stage summary fields
	"stage_duration",
	"scan_duration",
	"makemkv_duration",
	"tmdb_search_duration",
	"total_ripped_bytes",
	"ripped_size_bytes",
	"input_bytes",
	"output_bytes",
	"compression_ratio_percent",
	"final_file_size_bytes",
	"files_encoded",
	"titles_ripped",
	"title_count",
	"cache_used",
	"cache_decision",
	"identified",
	"media_type",
	"media_title",
	"reason",
}

// selectInfoFields returns formatted info-level fields and a count of hidden entries.
// limit=0 means no limit. includeDebug controls whether debug-only keys are allowed.
func selectInfoFields(attrs []kv, limit int, includeDebug bool) ([]infoField, int) {
	if len(attrs) == 0 {
		return nil, 0
	}
	if limit < 0 {
		limit = 0
	}
	used := make([]bool, len(attrs))
	formatted := make([]string, len(attrs))
	formattedSet := make([]bool, len(attrs))
	ensureValue := func(idx int) string {
		if !formattedSet[idx] {
			formatted[idx] = formatValueForKeyWithAttrs(attrs[idx].key, attrs[idx].value, attrs)
			formattedSet[idx] = true
		}
		return formatted[idx]
	}
	result := make([]infoField, 0, infoAttrLimit)
	hidden := 0

	for _, key := range infoHighlightKeys {
		if limit > 0 && len(result) >= limit {
			break
		}
		for idx, attr := range attrs {
			if used[idx] || attr.key != key {
				continue
			}
			used[idx] = true
			if skipInfoKey(attr.key) {
				break
			}
			if !includeDebug && isDebugOnlyKey(attr.key) {
				hidden++
				break
			}
			val := ensureValue(idx)
			if !includeDebug && shouldHideInfoValue(attr.key, val) {
				hidden++
				break
			}
			result = append(result, infoField{label: displayLabel(attr.key), value: val})
			break
		}
	}

	for idx, attr := range attrs {
		if used[idx] {
			continue
		}
		used[idx] = true
		if skipInfoKey(attr.key) {
			continue
		}
		if !includeDebug && isDebugOnlyKey(attr.key) {
			hidden++
			continue
		}
		val := ensureValue(idx)
		if !includeDebug && shouldHideInfoValue(attr.key, val) {
			hidden++
			continue
		}
		if limit <= 0 || len(result) < limit {
			result = append(result, infoField{label: displayLabel(attr.key), value: val})
		} else if limit > 0 {
			hidden++
		}
	}

	return result, hidden
}

// formatValueForKey applies smart formatting based on the key name.
func formatValueForKeyWithAttrs(key string, v slog.Value, attrs []kv) string {
	v = v.Resolve()

	// Handle byte sizes
	if isByteSizeKey(key) && (v.Kind() == slog.KindInt64 || v.Kind() == slog.KindUint64) {
		var bytes int64
		if v.Kind() == slog.KindInt64 {
			bytes = v.Int64()
		} else {
			bytes = int64(v.Uint64())
		}
		return formatBytes(bytes)
	}

	// Handle durations
	if isDurationKey(key) && v.Kind() == slog.KindDuration {
		return formatDurationHuman(v.Duration())
	}

	// Handle percentages
	if isPercentKey(key) && v.Kind() == slog.KindFloat64 {
		return formatPercent(v.Float64())
	}

	// Handle booleans with friendlier display
	if v.Kind() == slog.KindBool {
		if v.Bool() {
			return "yes"
		}
		return "no"
	}

	value := formatValue(v)
	if key == "error" || key == "error_message" {
		detailPath := attrValue(attrs, FieldErrorDetailPath)
		value = truncateErrorValue(value, detailPath)
	}
	return value
}

// isByteSizeKey returns true if the key represents a byte size.
func isByteSizeKey(key string) bool {
	return strings.HasSuffix(key, "_bytes") ||
		strings.HasSuffix(key, "_size") ||
		key == "size" ||
		key == "input_bytes" ||
		key == "output_bytes"
}

// isDurationKey returns true if the key represents a duration.
func isDurationKey(key string) bool {
	return strings.HasSuffix(key, "_duration") ||
		strings.HasSuffix(key, "_elapsed") ||
		strings.HasSuffix(key, "_latency") ||
		key == "elapsed" ||
		key == "duration" ||
		key == "backoff"
}

// isPercentKey returns true if the key represents a percentage.
func isPercentKey(key string) bool {
	return strings.HasSuffix(key, "_percent") ||
		strings.HasSuffix(key, "_ratio_percent") ||
		key == FieldProgressPercent
}

func truncateErrorValue(value, detailPath string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	const maxLen = 200
	if len(value) > maxLen {
		value = value[:maxLen] + "…"
	}
	if strings.TrimSpace(detailPath) != "" {
		if !strings.Contains(value, "error_detail_path") && !strings.Contains(value, "detail_path") {
			value += " (see error_detail_path)"
		}
	}
	return value
}

func skipInfoKey(key string) bool {
	switch key {
	case "", FieldItemID, FieldStage, FieldLane, "component":
		return true
	default:
		return false
	}
}

func isDebugOnlyKey(key string) bool {
	if key == "" {
		return true
	}
	switch key {
	case FieldCorrelationID,
		"fingerprint",
		"device",
		"source_path",
		"destination_dir",
		"tmdb_id",
		"runtime_range",
		"volume_identifier",
		"bd_info_available",
		"makemkv_enabled",
		"is_blu_ray",
		"has_aacs",
		"vote_average",
		"vote_count",
		"popularity",
		"segments",
		"segment_count",
		"token_count",
		"downloads",
		"score",
		"score_reasons",
		"size_mb",
		"duration_seconds",
		"intro_gap_seconds",
		"tail_delta_seconds":
		return true
	}
	if strings.Contains(key, "correlation") {
		return true
	}
	if strings.HasSuffix(key, "_id") && key != FieldItemID {
		return true
	}
	if strings.HasPrefix(key, "ffprobe.") {
		return true
	}
	if strings.Contains(key, "_path") || strings.Contains(key, "_dir") {
		return true
	}
	if strings.Contains(key, "fingerprint") || strings.Contains(key, "tmdb") {
		return true
	}
	return false
}

func shouldHideInfoValue(key, value string) bool {
	switch key {
	case "error_message", "error", "command", "preset_reason":
		return false
	}
	return len(value) > 120
}

func displayLabel(key string) string {
	switch key {
	case FieldAlert:
		return "Alert"
	case FieldEventType:
		return "Event"
	case FieldDecisionType:
		return "Decision"
	case FieldErrorCode:
		return "Error Code"
	case FieldErrorHint:
		return "Hint"
	case FieldErrorDetailPath:
		return "Error Detail"
	case FieldItemID:
		return "Item"
	case FieldStage:
		return "Stage"
	case "disc_title":
		return "Disc"
	case "disc_label":
		return "Label"
	case "processing_status":
		return "Status"
	case "progress_stage":
		return "Progress Stage"
	case "progress_message":
		return "Progress"
	case "disc_type":
		return "Type"
	case "runtime_minutes":
		return "Runtime"
	// Stage summary fields - concise labels
	case "stage_duration":
		return "Duration"
	case "scan_duration":
		return "Scan Time"
	case "makemkv_duration":
		return "Rip Time"
	case "tmdb_search_duration":
		return "TMDB Lookup"
	case "total_ripped_bytes", "ripped_size_bytes":
		return "Ripped Size"
	case "input_bytes":
		return "Input"
	case "output_bytes":
		return "Output"
	case "compression_ratio_percent":
		return "Compression"
	case "final_file_size_bytes":
		return "File Size"
	case "files_encoded":
		return "Files"
	case "titles_ripped", "title_count":
		return "Titles"
	case "cache_used":
		return "Cache Hit"
	case "cache_decision":
		return "Cache"
	case "identified":
		return "Identified"
	case "media_type":
		return "Type"
	case "media_title":
		return "Title"
	case "identified_title":
		return "Title"
	case "queries_attempted":
		return "Queries"
	case "needs_review":
		return "Needs Review"
	case "preset_profile":
		return "Preset"
	case "decision_result":
		return "Decision"
	case "decision_selected":
		return "Selected"
	case "decision_candidates":
		return "Candidates"
	case "decision_rejects":
		return "Rejected"
	case "is_movie":
		return "Movie"
	case "reason":
		return "Reason"
	case "opensubtitles":
		return "OpenSubtitles"
	case "whisperx_fallback":
		return "WhisperX"
	case "episodes":
		return "Episodes"
	default:
		return titleizeKey(key)
	}
}

func titleizeKey(key string) string {
	if key == "" {
		return ""
	}
	parts := strings.FieldsFunc(key, func(r rune) bool {
		return r == '_' || r == '-'
	})
	if len(parts) == 0 {
		return strings.ToUpper(key[:1]) + strings.ToLower(key[1:])
	}
	for i, part := range parts {
		parts[i] = capitalizeASCII(part)
	}
	return strings.Join(parts, " ")
}

func capitalizeASCII(value string) string {
	switch len(value) {
	case 0:
		return ""
	case 1:
		return strings.ToUpper(value)
	default:
		lower := strings.ToLower(value)
		return strings.ToUpper(lower[:1]) + lower[1:]
	}
}

func infoSummaryKey(component, itemID, _ string, attrs []kv) string {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		if disc := attrValue(attrs, "disc_title"); disc != "" {
			itemID = "disc:" + disc
		} else if label := attrValue(attrs, "disc_label"); label != "" {
			itemID = "label:" + label
		} else if component != "" {
			itemID = component
		}
	}
	if itemID == "" {
		return ""
	}
	return itemID
}

func attrValue(attrs []kv, key string) string {
	for _, kv := range attrs {
		if kv.key == key {
			return attrString(kv.value)
		}
	}
	return ""
}
