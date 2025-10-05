package logging

import "strings"

type infoField struct {
	label string
	value string
}

const infoAttrLimit = 4

var infoHighlightKeys = []string{
	"disc_title",
	"disc_label",
	"processing_status",
	"progress_stage",
	"progress_percent",
	"progress_message",
	"progress_eta",
	"command",
	"error_message",
	"status",
	"disc_type",
	"runtime_minutes",
	"studio",
}

func selectInfoFields(attrs []kv) ([]infoField, int) {
	if len(attrs) == 0 {
		return nil, 0
	}
	used := make([]bool, len(attrs))
	formatted := make([]string, len(attrs))
	formattedSet := make([]bool, len(attrs))
	ensureValue := func(idx int) string {
		if !formattedSet[idx] {
			formatted[idx] = formatValue(attrs[idx].value)
			formattedSet[idx] = true
		}
		return formatted[idx]
	}
	result := make([]infoField, 0, infoAttrLimit)
	hidden := 0

	for _, key := range infoHighlightKeys {
		if len(result) >= infoAttrLimit {
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
			if isDebugOnlyKey(attr.key) {
				hidden++
				break
			}
			val := ensureValue(idx)
			if shouldHideInfoValue(attr.key, val) {
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
		if isDebugOnlyKey(attr.key) {
			hidden++
			continue
		}
		val := ensureValue(idx)
		if shouldHideInfoValue(attr.key, val) {
			hidden++
			continue
		}
		if len(result) < infoAttrLimit {
			result = append(result, infoField{label: displayLabel(attr.key), value: val})
		} else {
			hidden++
		}
	}

	return result, hidden
}

func skipInfoKey(key string) bool {
	switch key {
	case "", FieldItemID, FieldStage, "component":
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
		"popularity":
		return true
	}
	if strings.Contains(key, "correlation") {
		return true
	}
	if strings.HasSuffix(key, "_id") && key != FieldItemID {
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
	case "error_message", "error", "command":
		return false
	}
	return len(value) > 120
}

func displayLabel(key string) string {
	switch key {
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
