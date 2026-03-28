package queue

import (
	"fmt"
	"strings"
)

// FormatAlsoProcessing returns a suffix describing other in-progress items,
// excluding the item with excludeID. Returns empty string if no others are active.
func FormatAlsoProcessing(store *Store, excludeID int64) string {
	items, err := store.InProgressItems()
	if err != nil {
		return ""
	}

	var others []string
	for _, it := range items {
		if it.ID == excludeID {
			continue
		}
		title := it.DiscTitle
		if title == "" {
			title = fmt.Sprintf("Item %d", it.ID)
		}
		others = append(others, fmt.Sprintf("%s (%s)", title, it.Stage))
	}

	if len(others) == 0 {
		return ""
	}

	return "\nAlso processing: " + strings.Join(others, ", ")
}
