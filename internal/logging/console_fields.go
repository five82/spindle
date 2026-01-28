package logging

import (
	"log/slog"
	"sort"
	"strconv"
	"strings"
)

type infoField struct {
	key   string
	label string
	value string
}

type detailLine struct {
	indent int
	label  string
	value  string
}

const infoAttrLimit = 8

var infoHighlightKeys = []string{
	FieldAlert,
	FieldEventType,
	FieldDecisionType,
	"disc_title",
	"disc_label",
	"processing_status",
	"next_status",
	"source_file",
	"ripped_file",
	"encoded_file",
	"final_file",
	"log_file",
	"destination",
	"subtitle_file",
	FieldProgressStage,
	FieldProgressPercent,
	FieldProgressMessage,
	FieldProgressETA,
	"command",
	"error_message",
	FieldImpact,
	"next_step",
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
	"review_reason",
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
			result = append(result, infoField{key: attr.key, label: displayLabel(attr.key), value: val})
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
			result = append(result, infoField{key: attr.key, label: displayLabel(attr.key), value: val})
		} else if limit > 0 {
			hidden++
		}
	}

	return reorderInfoFields(result), hidden
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
		return FormatBytes(bytes)
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
	case FieldImpact:
		return "Impact"
	case "next_step":
		return "Next Step"
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
	case "next_status":
		return "Next Status"
	case "source_file":
		return "Source"
	case "ripped_file":
		return "Ripped"
	case "encoded_file":
		return "Encoded"
	case "final_file":
		return "Final"
	case "log_file":
		return "Log"
	case "destination":
		return "Destination"
	case "subtitle_file":
		return "Subtitle"
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
	case "review_reason":
		return "Review Reason"
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

func reorderInfoFields(fields []infoField) []infoField {
	if len(fields) == 0 {
		return fields
	}
	priorityKeys := []string{
		FieldEventType,
		FieldDecisionType,
		"decision_result",
		"decision_reason",
		"decision_options",
		"decision_selected",
	}
	groups := infoGroupSpecs()

	buckets := make(map[string]*groupBucket, len(groups))
	isGrouped := func(key string) (string, bool) {
		for _, group := range groups {
			if group.countKey != "" && key == group.countKey {
				return group.name, true
			}
			if group.itemPrefix != "" && strings.HasPrefix(key, group.itemPrefix) {
				return group.name, true
			}
			if group.name != "" && key == group.name+"_hidden_count" {
				return group.name, true
			}
		}
		return "", false
	}

	nonGroup := make([]infoField, 0, len(fields))
	for _, field := range fields {
		if field.key == "" {
			nonGroup = append(nonGroup, field)
			continue
		}
		groupName, ok := isGrouped(field.key)
		if !ok {
			nonGroup = append(nonGroup, field)
			continue
		}
		bucket := buckets[groupName]
		if bucket == nil {
			bucket = &groupBucket{}
			buckets[groupName] = bucket
		}
		if strings.HasSuffix(field.key, "_count") {
			fieldCopy := field
			bucket.count = &fieldCopy
			continue
		}
		bucket.items = append(bucket.items, field)
	}

	out := make([]infoField, 0, len(fields))
	used := make([]bool, len(nonGroup))
	for _, key := range priorityKeys {
		for idx, field := range nonGroup {
			if used[idx] || field.key != key {
				continue
			}
			out = append(out, field)
			used[idx] = true
			break
		}
	}
	for idx, field := range nonGroup {
		if used[idx] {
			continue
		}
		out = append(out, field)
	}

	for _, group := range groups {
		bucket := buckets[group.name]
		if bucket == nil {
			continue
		}
		if bucket.count != nil {
			out = append(out, *bucket.count)
		}
		if len(bucket.items) == 0 {
			continue
		}
		sort.Slice(bucket.items, func(i, j int) bool {
			leftKey := bucket.items[i].key
			rightKey := bucket.items[j].key
			leftNum, leftOK := suffixNumber(leftKey)
			rightNum, rightOK := suffixNumber(rightKey)
			if leftOK && rightOK && leftNum != rightNum {
				return leftNum < rightNum
			}
			if leftOK != rightOK {
				return leftOK
			}
			return leftKey < rightKey
		})
		out = append(out, bucket.items...)
	}

	return out
}

type groupSpec struct {
	name       string
	countKey   string
	itemPrefix string
	label      string
}

type groupBucket struct {
	count *infoField
	items []infoField
}

func infoGroupSpecs() []groupSpec {
	return []groupSpec{
		{name: "removed", countKey: "removed_count", itemPrefix: "removed_", label: "Removed"},
		{name: "job", countKey: "job_count", itemPrefix: "job_", label: "Jobs"},
		{name: "source", countKey: "source_count", itemPrefix: "source_", label: "Sources"},
		{name: "mode", countKey: "mode_count", itemPrefix: "mode_", label: "Modes"},
		{name: "title", countKey: "title_count", itemPrefix: "title_", label: "Titles"},
		{name: "candidate", countKey: "candidate_count", itemPrefix: "candidate_", label: "Candidates"},
		{name: "accepted", countKey: "accepted_count", itemPrefix: "accepted_", label: "Accepted"},
		{name: "selected", countKey: "selected_count", itemPrefix: "selected_", label: "Selected"},
		{name: "rejected", countKey: "rejected_count", itemPrefix: "rejected_", label: "Rejected"},
		{name: "reason", countKey: "reason_count", itemPrefix: "reason_", label: "Reason Counts"},
		{name: "prefilter_reject", countKey: "", itemPrefix: "prefilter_reject_", label: "Prefilter Rejections"},
		{name: "prefilter_reason", countKey: "prefilter_reason_count", itemPrefix: "prefilter_reason_", label: "Prefilter Reason Counts"},
		{name: "thresholds", countKey: "", itemPrefix: "thresholds.", label: "Thresholds"},
	}
}

type detailLineOptions struct {
	friendlyLabels bool
}

func buildInfoLines(fields []infoField) []detailLine {
	return buildDetailLines(fields, detailLineOptions{friendlyLabels: true})
}

func buildDebugLines(fields []infoField) []detailLine {
	return buildDetailLines(fields, detailLineOptions{friendlyLabels: false})
}

func buildDetailLines(fields []infoField, opts detailLineOptions) []detailLine {
	if len(fields) == 0 {
		return nil
	}
	ordered := fields
	if opts.friendlyLabels {
		ordered = reorderInfoFields(fields)
	}
	groupSpecs := infoGroupSpecs()
	groupBuckets := make(map[string]*groupBucket, len(groupSpecs))
	findGroup := func(key string) (groupSpec, bool, bool) {
		for _, spec := range groupSpecs {
			if spec.countKey != "" && key == spec.countKey {
				return spec, true, true
			}
			if spec.itemPrefix != "" && strings.HasPrefix(key, spec.itemPrefix) {
				return spec, true, false
			}
		}
		return groupSpec{}, false, false
	}

	nonGroup := make([]infoField, 0, len(fields))
	for _, field := range ordered {
		spec, ok, isCount := findGroup(field.key)
		if !ok {
			nonGroup = append(nonGroup, field)
			continue
		}
		bucket := groupBuckets[spec.name]
		if bucket == nil {
			bucket = &groupBucket{}
			groupBuckets[spec.name] = bucket
		}
		if isCount {
			fieldCopy := field
			bucket.count = &fieldCopy
			continue
		}
		bucket.items = append(bucket.items, field)
	}

	lines := make([]detailLine, 0, len(fields))
	for _, field := range nonGroup {
		lines = append(lines, detailLine{indent: 1, label: field.label, value: field.value})
	}
	for _, spec := range groupSpecs {
		bucket := groupBuckets[spec.name]
		if bucket == nil {
			continue
		}
		if len(bucket.items) == 0 {
			if bucket.count == nil {
				continue
			}
			lines = append(lines, detailLine{indent: 1, label: groupLabel(spec), value: bucket.count.value})
			continue
		}
		headerValue := ""
		if bucket.count != nil {
			headerValue = bucket.count.value
		}
		lines = append(lines, detailLine{indent: 1, label: groupLabel(spec), value: headerValue})
		for _, item := range bucket.items {
			lines = append(lines, detailLine{indent: 2, label: groupItemLabel(spec, item, opts.friendlyLabels), value: item.value})
		}
	}
	return lines
}

func groupLabel(spec groupSpec) string {
	if spec.label != "" {
		return spec.label
	}
	return titleizeKey(spec.name)
}

func groupItemLabel(spec groupSpec, field infoField, friendly bool) string {
	if spec.itemPrefix != "" && strings.HasPrefix(field.key, spec.itemPrefix) {
		if n, ok := suffixNumber(field.key); ok {
			return "#" + strconv.Itoa(n)
		}
		suffix := strings.TrimPrefix(field.key, spec.itemPrefix)
		if suffix != "" {
			if friendly {
				suffix = strings.ReplaceAll(suffix, ".", "_")
				return titleizeKey(suffix)
			}
			return suffix
		}
	}
	return field.label
}

func suffixNumber(key string) (int, bool) {
	idx := strings.LastIndex(key, "_")
	if idx == -1 || idx == len(key)-1 {
		return 0, false
	}
	value, err := strconv.Atoi(key[idx+1:])
	if err != nil {
		return 0, false
	}
	return value, true
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
