package queue

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/five82/spindle/internal/mediameta"
	"github.com/five82/spindle/internal/textutil"
)

// parseTimestamp parses the timestamp strings this package's rows carry.
// Columns are SQLite CURRENT_TIMESTAMP values; the sqlite driver surfaces
// TIMESTAMP columns as time.Time, which database/sql renders into string
// fields as RFC3339Nano, so that is the form scans normally produce. The
// raw SQLite layout ("2006-01-02 15:04:05", UTC) is kept as a fallback.
// The format knowledge is owned here; consumers must go through
// CreatedTime/Duration instead of parsing the raw strings.
func parseTimestamp(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC)
}

// Item represents a single queue item with all database columns.
// Progress lives on task rows (Task.Progress*), not here: each running
// handler reports against its own task, so concurrent branches of one item
// never share a progress slot.
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
	EncodingDetailsJSON string
	userStopped         int
}

// UserStopped reports whether the item was explicitly stopped by the user.
func (it *Item) UserStopped() bool {
	return it != nil && it.userStopped != 0
}

// CreatedTime parses the item's creation timestamp; ok is false when the
// stored value is missing or unparseable.
func (it *Item) CreatedTime() (created time.Time, ok bool) {
	t, err := parseTimestamp(it.CreatedAt)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
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

	meta := mediameta.FromJSON(it.MetadataJSON, "")
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
