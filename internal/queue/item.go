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
