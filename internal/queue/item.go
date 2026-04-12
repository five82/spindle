package queue

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/five82/spindle/internal/textutil"
)

// Item represents a single queue item with all database columns.
type Item struct {
	ID                  int64
	DiscTitle           string
	Stage               Stage
	InProgress          int
	FailedAtStage       string
	ErrorMessage        string
	CreatedAt           string
	UpdatedAt           string
	RipSpecData         string
	DiscFingerprint     string
	MetadataJSON        string
	NeedsReview         int
	ReviewReason        string
	ProgressStage       string
	ProgressPercent     float64
	ProgressMessage     string
	ActiveEpisodeKey    string
	ProgressBytesCopied int64
	ProgressTotalBytes  int64
	EncodingDetailsJSON string
	RippedFile          string
	EncodedFile         string
	FinalFile           string
}

// StagingRoot computes the per-item working directory under base.
// If DiscFingerprint is non-empty, the uppercase fingerprint is used as the
// directory name. Otherwise "queue-{ID}" is used.
func (it *Item) StagingRoot(base string) (string, error) {
	var segment string
	if it.DiscFingerprint != "" {
		segment = strings.ToUpper(it.DiscFingerprint)
	} else {
		segment = fmt.Sprintf("queue-%d", it.ID)
	}
	segment = textutil.SanitizePathSegment(segment)
	return textutil.SafeJoin(base, segment)
}

// AppendReviewReason sets NeedsReview=1 and appends reason to the ReviewReason
// JSON array. If ReviewReason is empty or not valid JSON, a new array is created.
func (it *Item) AppendReviewReason(reason string) {
	it.NeedsReview = 1

	var reasons []string
	if it.ReviewReason != "" {
		_ = json.Unmarshal([]byte(it.ReviewReason), &reasons)
	}
	if reasons == nil {
		reasons = []string{}
	}
	reasons = append(reasons, reason)

	data, err := json.Marshal(reasons)
	if err != nil {
		// Should never happen with []string, but be safe.
		it.ReviewReason = `["` + reason + `"]`
		return
	}
	it.ReviewReason = string(data)
}

// ReviewReasons returns the parsed review reasons. Invalid JSON returns nil.
func (it *Item) ReviewReasons() []string {
	if strings.TrimSpace(it.ReviewReason) == "" {
		return nil
	}
	var reasons []string
	if err := json.Unmarshal([]byte(it.ReviewReason), &reasons); err != nil {
		return nil
	}
	return reasons
}

// PrimaryReviewReason returns the first review reason, if any.
func (it *Item) PrimaryReviewReason() string {
	reasons := it.ReviewReasons()
	if len(reasons) == 0 {
		return ""
	}
	return reasons[0]
}

// ReviewSummary returns a compact review summary, capped to maxReasons entries.
func (it *Item) ReviewSummary(maxReasons int) string {
	reasons := it.ReviewReasons()
	if len(reasons) == 0 {
		return ""
	}
	if maxReasons <= 0 || maxReasons >= len(reasons) {
		return strings.Join(reasons, "; ")
	}
	summary := strings.Join(reasons[:maxReasons], "; ")
	remaining := len(reasons) - maxReasons
	if remaining > 0 {
		summary += fmt.Sprintf("; +%d more", remaining)
	}
	return summary
}

// DisplayTitle returns the best available user-facing title for the item.
func (it *Item) DisplayTitle() string {
	if title := strings.TrimSpace(it.DiscTitle); title != "" {
		return title
	}

	meta := MetadataFromJSON(it.MetadataJSON, "")
	if title := strings.TrimSpace(meta.DisplayTitle); title != "" {
		if meta.Year != "" && !strings.Contains(title, "(") {
			return title + " (" + meta.Year + ")"
		}
		return title
	}
	if title := strings.TrimSpace(meta.ShowTitle); title != "" {
		if meta.SeasonNumber > 0 {
			return fmt.Sprintf("%s Season %02d", title, meta.SeasonNumber)
		}
		return title
	}
	if title := strings.TrimSpace(meta.Title); title != "" {
		if meta.Year != "" {
			return title + " (" + meta.Year + ")"
		}
		return title
	}
	if it.ID > 0 {
		return fmt.Sprintf("Item %d", it.ID)
	}
	return "Unknown Item"
}
