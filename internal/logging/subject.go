package logging

import "strings"

// FormatSubject builds the lane/item/stage subject string used in console output.
func FormatSubject(lane, itemID, stage string) string {
	lane = strings.TrimSpace(lane)
	itemID = strings.TrimSpace(itemID)
	stage = strings.TrimSpace(stage)
	parts := make([]string, 0, 3)
	if lane != "" {
		var formattedLane string
		if len(lane) > 1 {
			formattedLane = strings.ToUpper(lane[:1]) + strings.ToLower(lane[1:])
		} else {
			formattedLane = strings.ToUpper(lane)
		}
		parts = append(parts, formattedLane)
	}
	switch {
	case itemID != "" && stage != "":
		parts = append(parts, "Item #"+itemID+" ("+stage+")")
	case itemID != "":
		parts = append(parts, "Item #"+itemID)
	case stage != "":
		parts = append(parts, stage)
	}
	return strings.Join(parts, " · ")
}
