package subtitle

import (
	"strings"
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestSubtitleValidationReviewPersistsOnlyActionableIssues(t *testing.T) {
	sess := newSubtitleTestSession(t, &ripspec.Envelope{Episodes: []ripspec.Episode{{Key: "s01e01"}}})
	applySubtitleReviewIssues(sess.Logger, sess, "s01e01", validationResult{
		Issues: []string{"observation", "bad timing"}, ReviewIssues: []string{"bad timing"},
	})
	item, err := sess.Store.GetByID(sess.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	if got := env.Episodes[0].ReviewReason; !strings.Contains(got, "bad timing") || strings.Contains(got, "observation") {
		t.Fatalf("episode review = %q", got)
	}
	if got := item.ReviewReason; !strings.Contains(got, "srt_validation: bad timing") || strings.Contains(got, "observation") {
		t.Fatalf("queue review = %q", got)
	}
}

func TestSubtitleFailureRecordsAssetAndReview(t *testing.T) {
	sess := newSubtitleTestSession(t, &ripspec.Envelope{Episodes: []ripspec.Episode{{Key: "main"}}})
	recordSubtitleFailure(sess.Logger, sess, "main", "  ")
	item, err := sess.Store.GetByID(sess.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	asset, ok := env.Assets.FindAsset(ripspec.AssetKindSubtitled, "main")
	if !ok || !asset.IsFailed() || asset.ErrorMsg != "subtitle generation failed" {
		t.Fatalf("failed asset = %+v, found %v", asset, ok)
	}
	if !strings.Contains(env.Episodes[0].ReviewReason, asset.ErrorMsg) || !strings.Contains(item.ReviewReason, "subtitle_failure:") {
		t.Fatalf("missing reviews: episode=%q item=%q", env.Episodes[0].ReviewReason, item.ReviewReason)
	}
	// Persistence errors must not stop other episodes from being processed.
	if err := sess.Store.Close(); err != nil {
		t.Fatal(err)
	}
	recordSubtitleFailure(sess.Logger, sess, "other", "disk unavailable")
}
