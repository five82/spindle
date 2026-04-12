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
		others = append(others, fmt.Sprintf("%s (%s)", it.DisplayTitle(), HumanStage(it.Stage)))
	}

	if len(others) == 0 {
		return ""
	}

	const maxVisible = 2
	if len(others) > maxVisible {
		remaining := len(others) - maxVisible
		others = append(others[:maxVisible], fmt.Sprintf("+%d more", remaining))
	}

	return "\nAlso processing: " + strings.Join(others, ", ")
}
