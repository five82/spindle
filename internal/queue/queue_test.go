package queue

import (
	"encoding/json"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestNewDiscDefaults(t *testing.T) {
	store := openTestStore(t)

	item, err := store.NewDisc("Test Disc", "abc123")
	if err != nil {
		t.Fatalf("new disc: %v", err)
	}
	if item.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if item.Stage != StageIdentification {
		t.Errorf("stage = %q, want %q", item.Stage, StageIdentification)
	}
	if item.DiscTitle != "Test Disc" {
		t.Errorf("disc_title = %q, want %q", item.DiscTitle, "Test Disc")
	}
	if item.DiscFingerprint != "abc123" {
		t.Errorf("fingerprint = %q, want %q", item.DiscFingerprint, "abc123")
	}
	if item.InProgress != 0 {
		t.Errorf("in_progress = %d, want 0", item.InProgress)
	}
	if item.NeedsReview != 0 {
		t.Errorf("needs_review = %d, want 0", item.NeedsReview)
	}
}

func TestNewCachedRipStartsAtRipping(t *testing.T) {
	store := openTestStore(t)

	item, err := store.NewCachedRip("Cached Disc", "fp-cache", `{"version":1}`, `{"title":"Cached Disc"}`)
	if err != nil {
		t.Fatalf("new cached rip: %v", err)
	}
	if item.Stage != StageRipping {
		t.Fatalf("stage = %q, want %q", item.Stage, StageRipping)
	}
	if item.RipSpecData != `{"version":1}` || item.MetadataJSON == "" {
		t.Fatalf("cached work state not persisted: rip_spec=%q metadata=%q", item.RipSpecData, item.MetadataJSON)
	}
}

func TestGetByIDFound(t *testing.T) {
	store := openTestStore(t)

	created, err := store.NewDisc("Test", "fp1")
	if err != nil {
		t.Fatalf("new disc: %v", err)
	}

	found, err := store.GetByID(created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if found == nil {
		t.Fatal("expected item, got nil")
	}
	if found.ID != created.ID {
		t.Errorf("id = %d, want %d", found.ID, created.ID)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	store := openTestStore(t)

	item, err := store.GetByID(9999)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if item != nil {
		t.Errorf("expected nil, got item %d", item.ID)
	}
}

func TestFindByFingerprint(t *testing.T) {
	store := openTestStore(t)

	_, err := store.NewDisc("Disc A", "fingerprint-abc")
	if err != nil {
		t.Fatalf("new disc: %v", err)
	}

	found, err := store.FindByFingerprint("fingerprint-abc")
	if err != nil {
		t.Fatalf("find by fingerprint: %v", err)
	}
	if found == nil {
		t.Fatal("expected item, got nil")
	}
	if found.DiscTitle != "Disc A" {
		t.Errorf("title = %q, want %q", found.DiscTitle, "Disc A")
	}

	notFound, err := store.FindByFingerprint("nonexistent")
	if err != nil {
		t.Fatalf("find by fingerprint: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for nonexistent fingerprint")
	}
}

func TestLifecycleAndTitleUpdates(t *testing.T) {
	store := openTestStore(t)

	item, err := store.NewDisc("Original", "fp1")
	if err != nil {
		t.Fatalf("new disc: %v", err)
	}
	if err := store.MoveToStage(item, StageEncoding); err != nil {
		t.Fatalf("move stage: %v", err)
	}
	if err := store.StartStage(item, StageEncoding); err != nil {
		t.Fatalf("start stage: %v", err)
	}
	if err := store.UpdateDiscTitle(item, "Updated Title"); err != nil {
		t.Fatalf("update title: %v", err)
	}

	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Stage != StageEncoding {
		t.Errorf("stage = %q, want %q", got.Stage, StageEncoding)
	}
	if got.InProgress != 1 {
		t.Errorf("in_progress = %d, want 1", got.InProgress)
	}
	if got.DiscTitle != "Updated Title" {
		t.Errorf("title = %q, want %q", got.DiscTitle, "Updated Title")
	}
}

func TestUpdateProgress(t *testing.T) {
	store := openTestStore(t)

	item, err := store.NewDisc("Disc", "fp1")
	if err != nil {
		t.Fatalf("new disc: %v", err)
	}

	item.ProgressStage = "encoding"
	item.ProgressPercent = 42.5
	item.ProgressMessage = "Encoding track 1"
	item.ProgressBytesCopied = 1000
	item.ProgressTotalBytes = 5000
	item.ActiveEpisodeKey = "s01e03"
	item.EncodingDetailsJSON = `{"speed": 1.5}`

	if err := store.UpdateProgress(item); err != nil {
		t.Fatalf("update progress: %v", err)
	}

	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ProgressPercent != 42.5 {
		t.Errorf("percent = %f, want 42.5", got.ProgressPercent)
	}
	if got.ProgressMessage != "Encoding track 1" {
		t.Errorf("message = %q, want %q", got.ProgressMessage, "Encoding track 1")
	}
	if got.ActiveEpisodeKey != "s01e03" {
		t.Errorf("episode key = %q, want %q", got.ActiveEpisodeKey, "s01e03")
	}
}

func TestListWithAndWithoutFilter(t *testing.T) {
	store := openTestStore(t)

	item1, _ := store.NewDisc("A", "fp1")
	item2, _ := store.NewDisc("B", "fp2")
	_ = store.MoveToStage(item2, StageEncoding)

	// All items.
	all, err := store.List()
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("list all = %d items, want 2", len(all))
	}

	// Filter by identification stage.
	identification, err := store.List(StageIdentification)
	if err != nil {
		t.Fatalf("list identification: %v", err)
	}
	if len(identification) != 1 {
		t.Errorf("list identification = %d items, want 1", len(identification))
	}
	if identification[0].ID != item1.ID {
		t.Errorf("identification item id = %d, want %d", identification[0].ID, item1.ID)
	}

	// Filter by multiple statuses.
	multi, err := store.List(StageIdentification, StageEncoding)
	if err != nil {
		t.Fatalf("list multi: %v", err)
	}
	if len(multi) != 2 {
		t.Errorf("list multi = %d items, want 2", len(multi))
	}
}

func TestNextForStatuses(t *testing.T) {
	store := openTestStore(t)

	item1, _ := store.NewDisc("A", "fp1")
	item2, _ := store.NewDisc("B", "fp2")
	// Mark item1 as in progress.
	_ = store.StartStage(item1, StageIdentification)

	// Should skip item1 (in_progress=1) and return item2.
	next, err := store.NextForStatuses(StageIdentification)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if next == nil {
		t.Fatal("expected item, got nil")
	}
	if next.ID != item2.ID {
		t.Errorf("next id = %d, want %d", next.ID, item2.ID)
	}

	// No matching status.
	none, err := store.NextForStatuses(StageCompleted)
	if err != nil {
		t.Fatalf("next none: %v", err)
	}
	if none != nil {
		t.Error("expected nil for no matches")
	}
}

func TestActiveFingerprints(t *testing.T) {
	store := openTestStore(t)

	_, _ = store.NewDisc("A", "fp-aaa")
	_, _ = store.NewDisc("B", "fp-bbb")
	_, _ = store.NewDisc("C", "") // Empty fingerprint, should be excluded.

	fps, err := store.ActiveFingerprints()
	if err != nil {
		t.Fatalf("active fingerprints: %v", err)
	}
	if len(fps) != 2 {
		t.Errorf("got %d fingerprints, want 2", len(fps))
	}
	if _, ok := fps["fp-aaa"]; !ok {
		t.Error("missing fp-aaa")
	}
	if _, ok := fps["fp-bbb"]; !ok {
		t.Error("missing fp-bbb")
	}
}

func TestStats(t *testing.T) {
	store := openTestStore(t)

	_, _ = store.NewDisc("A", "fp1")
	_, _ = store.NewDisc("B", "fp2")
	item3, _ := store.NewDisc("C", "fp3")
	_ = store.MoveToStage(item3, StageEncoding)

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats[StageIdentification] != 2 {
		t.Errorf("identification = %d, want 2", stats[StageIdentification])
	}
	if stats[StageEncoding] != 1 {
		t.Errorf("encoding = %d, want 1", stats[StageEncoding])
	}
}

func TestResetInProgress(t *testing.T) {
	store := openTestStore(t)

	item, _ := store.NewDisc("A", "fp1")
	_ = store.StartStage(item, StageIdentification)

	if err := store.ResetInProgress(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	got, _ := store.GetByID(item.ID)
	if got.InProgress != 0 {
		t.Errorf("in_progress = %d, want 0", got.InProgress)
	}
}

func TestRetryFailedAll(t *testing.T) {
	store := openTestStore(t)

	failed1, _ := store.NewDisc("A", "fp1")
	_ = store.FailStage(failed1, StageEncoding, "encode error")

	failed2, _ := store.NewDisc("B", "fp2")
	_ = store.FailStage(failed2, StageSubtitling, "subtitle error")

	active, _ := store.NewDisc("C", "fp3")
	_ = store.MoveToStage(active, StageRipping)

	count, err := store.RetryFailed()
	if err != nil {
		t.Fatalf("retry all failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("retry count = %d, want 2", count)
	}

	got1, _ := store.GetByID(failed1.ID)
	if got1.Stage != StageEncoding {
		t.Errorf("failed1 stage = %q, want %q", got1.Stage, StageEncoding)
	}
	got2, _ := store.GetByID(failed2.ID)
	if got2.Stage != StageSubtitling {
		t.Errorf("failed2 stage = %q, want %q", got2.Stage, StageSubtitling)
	}
	gotActive, _ := store.GetByID(active.ID)
	if gotActive.Stage != StageRipping {
		t.Errorf("active stage = %q, want %q", gotActive.Stage, StageRipping)
	}
}

func TestRetryFailedRouting(t *testing.T) {
	store := openTestStore(t)

	// Item with failed_at_stage set.
	item1, _ := store.NewDisc("A", "fp1")
	_ = store.FailStage(item1, StageEncoding, "encode error")

	// Item without failed_at_stage.
	item2, _ := store.NewDisc("B", "fp2")
	_, _ = store.StopItems(item2.ID)

	if _, err := store.RetryFailed(item1.ID, item2.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}

	got1, _ := store.GetByID(item1.ID)
	if got1.Stage != StageEncoding {
		t.Errorf("item1 stage = %q, want %q", got1.Stage, StageEncoding)
	}
	if got1.ErrorMessage != "" {
		t.Errorf("item1 error_message = %q, want empty", got1.ErrorMessage)
	}

	got2, _ := store.GetByID(item2.ID)
	if got2.Stage != StageIdentification {
		t.Errorf("item2 stage = %q, want %q", got2.Stage, StageIdentification)
	}
}

func TestStopItemsAndOverride(t *testing.T) {
	store := openTestStore(t)

	item, _ := store.NewDisc("A", "fp1")
	_ = store.MoveToStage(item, StageEncoding)
	_ = store.StartStage(item, StageEncoding)

	if _, err := store.StopItems(item.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}

	got, _ := store.GetByID(item.ID)
	if got.Stage != StageFailed {
		t.Errorf("stage = %q, want %q", got.Stage, StageFailed)
	}
	if got.NeedsReview != 1 {
		t.Errorf("needs_review = %d, want 1", got.NeedsReview)
	}
	if !got.UserStopped() {
		t.Errorf("user stopped flag not set")
	}

	var reasons []string
	if err := json.Unmarshal([]byte(got.ReviewReason), &reasons); err != nil {
		t.Fatalf("unmarshal review_reason: %v", err)
	}
	found := false
	for _, r := range reasons {
		if r == ReviewReasonUserStopped {
			found = true
		}
	}
	if !found {
		t.Errorf("review_reason %q does not contain %q", got.ReviewReason, ReviewReasonUserStopped)
	}

}

func TestLifecycleMethodsDoNotOverrideUserStoppedItem(t *testing.T) {
	store := openTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	if err := store.StartStage(item, StageEncoding); err != nil {
		t.Fatalf("start stage: %v", err)
	}
	if _, err := store.StopItems(item.ID); err != nil {
		t.Fatalf("stop item: %v", err)
	}

	if err := store.CompleteStage(item, StageCompleted, true); err != nil {
		t.Fatalf("complete stopped item: %v", err)
	}
	got, _ := store.GetByID(item.ID)
	if got.Stage != StageFailed || got.ReviewReason == "" || !got.UserStopped() {
		t.Fatalf("completion overrode stopped item: stage=%q review=%q user_stopped=%v", got.Stage, got.ReviewReason, got.UserStopped())
	}
	if item.Stage != StageFailed || !item.UserStopped() {
		t.Fatalf("completion left in-memory item inconsistent: stage=%q user_stopped=%v", item.Stage, item.UserStopped())
	}

	// StopItems records the stopped stage in failed_at_stage so retry
	// resumes there; a racing FailStage must not override that or attach
	// its error message.
	stoppedAt := string(StageIdentification)
	if err := store.FailStage(item, StageEncoding, "encode failed"); err != nil {
		t.Fatalf("fail stopped item: %v", err)
	}
	got, _ = store.GetByID(item.ID)
	if got.ErrorMessage != "" || got.FailedAtStage != stoppedAt {
		t.Fatalf("failure overrode stopped item details: failed_at=%q err=%q", got.FailedAtStage, got.ErrorMessage)
	}
	if item.ErrorMessage != "" || item.FailedAtStage != stoppedAt || !item.UserStopped() {
		t.Fatalf("failure left in-memory item inconsistent: failed_at=%q err=%q user_stopped=%v", item.FailedAtStage, item.ErrorMessage, item.UserStopped())
	}

	item.DiscTitle = "Changed by handler"
	item.RipSpecData = `{"version":1}`
	item.AppendReviewReason("handler review")
	if err := store.UpdateWorkState(item); err != nil {
		t.Fatalf("update stopped work state: %v", err)
	}
	got, _ = store.GetByID(item.ID)
	if got.DiscTitle != "A" || got.RipSpecData != "" || got.ReviewSummary(0) != ReviewReasonUserStopped {
		t.Fatalf("work state overrode stopped item: title=%q ripspec=%q review=%q", got.DiscTitle, got.RipSpecData, got.ReviewReason)
	}
}

func TestStagingRoot(t *testing.T) {
	tests := []struct {
		name        string
		fingerprint string
		id          int64
		wantSuffix  string
	}{
		{
			name:        "with fingerprint",
			fingerprint: "abc123def",
			id:          1,
			wantSuffix:  "ABC123DEF",
		},
		{
			name:        "without fingerprint",
			fingerprint: "",
			id:          42,
			wantSuffix:  "queue-42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := &Item{ID: tt.id, DiscFingerprint: tt.fingerprint}
			result, err := item.StagingRoot("/tmp/staging")
			if err != nil {
				t.Fatalf("staging root: %v", err)
			}
			want := "/tmp/staging/" + tt.wantSuffix
			if result != want {
				t.Errorf("staging root = %q, want %q", result, want)
			}
		})
	}
}

func TestAppendReviewReason(t *testing.T) {
	item := &Item{}

	// First reason.
	item.AppendReviewReason("low confidence")
	if item.NeedsReview != 1 {
		t.Errorf("needs_review = %d, want 1", item.NeedsReview)
	}
	var reasons []string
	_ = json.Unmarshal([]byte(item.ReviewReason), &reasons)
	if len(reasons) != 1 || reasons[0] != "low confidence" {
		t.Errorf("reasons = %v, want [low confidence]", reasons)
	}

	// Second reason.
	item.AppendReviewReason("missing episodes")
	_ = json.Unmarshal([]byte(item.ReviewReason), &reasons)
	if len(reasons) != 2 {
		t.Errorf("reasons length = %d, want 2", len(reasons))
	}
	if reasons[1] != "missing episodes" {
		t.Errorf("reasons[1] = %q, want %q", reasons[1], "missing episodes")
	}
}

func TestClearAndClearCompleted(t *testing.T) {
	store := openTestStore(t)

	item1, _ := store.NewDisc("A", "fp1")
	item2, _ := store.NewDisc("B", "fp2")
	_ = store.MoveToStage(item1, StageCompleted)

	// ClearCompleted should only remove completed items.
	if _, err := store.ClearCompleted(); err != nil {
		t.Fatalf("clear completed: %v", err)
	}
	all, _ := store.List()
	if len(all) != 1 {
		t.Errorf("after clear completed: %d items, want 1", len(all))
	}
	if all[0].ID != item2.ID {
		t.Errorf("remaining item id = %d, want %d", all[0].ID, item2.ID)
	}

	// Clear should remove everything.
	if _, err := store.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	all, _ = store.List()
	if len(all) != 0 {
		t.Errorf("after clear: %d items, want 0", len(all))
	}
}

func TestRemove(t *testing.T) {
	store := openTestStore(t)

	item, _ := store.NewDisc("A", "fp1")
	if err := store.Remove(item.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got, _ := store.GetByID(item.ID)
	if got != nil {
		t.Error("expected nil after remove")
	}
}

func TestHasDiscDependentItem(t *testing.T) {
	store := openTestStore(t)

	// No items.
	has, err := store.HasDiscDependentItem()
	if err != nil {
		t.Fatalf("has disc dependent: %v", err)
	}
	if has {
		t.Error("expected false with no items")
	}

	// Item in identification, in progress.
	item, _ := store.NewDisc("A", "fp1")
	_ = store.StartStage(item, StageIdentification)

	has, err = store.HasDiscDependentItem()
	if err != nil {
		t.Fatalf("has disc dependent: %v", err)
	}
	if !has {
		t.Error("expected true with identification in progress")
	}
}

func TestDisplayTitleUsesDiscTitleFirst(t *testing.T) {
	item := &Item{DiscTitle: "Avatar (2009)", ID: 7}
	if got := item.DisplayTitle(); got != "Avatar (2009)" {
		t.Fatalf("DisplayTitle() = %q, want %q", got, "Avatar (2009)")
	}
}

func TestReviewSummaryCapsReasons(t *testing.T) {
	item := &Item{}
	item.AppendReviewReason("low confidence identification")
	item.AppendReviewReason("subtitle validation")
	item.AppendReviewReason("missing metadata")

	if got := item.ReviewSummary(2); got != "low confidence identification; subtitle validation; +1 more" {
		t.Fatalf("ReviewSummary() = %q", got)
	}
}

func TestHumanStage(t *testing.T) {
	if got := HumanStage(StageEpisodeIdentification); got != "episode ID" {
		t.Fatalf("HumanStage() = %q, want %q", got, "episode ID")
	}
}

func TestFormatAlsoProcessingHumanizesAndCaps(t *testing.T) {
	store := openTestStore(t)
	item1, _ := store.NewDisc("Avatar (2009)", "fp1")
	item2, _ := store.NewDisc("Breaking Bad Season 01", "fp2")
	item3, _ := store.NewDisc("Fringe Season 01", "fp3")
	item4, _ := store.NewDisc("The Matrix (1999)", "fp4")

	_ = store.MoveToStage(item1, StageRipping)
	_ = store.StartStage(item1, StageRipping)
	_ = store.MoveToStage(item2, StageEncoding)
	_ = store.StartStage(item2, StageEncoding)
	_ = store.MoveToStage(item3, StageSubtitling)
	_ = store.StartStage(item3, StageSubtitling)
	_ = store.MoveToStage(item4, StageAudioAnalysis)
	_ = store.StartStage(item4, StageAudioAnalysis)

	got := FormatAlsoProcessing(store, item1.ID)
	want := "\nAlso processing: Breaking Bad Season 01 (encoding), Fringe Season 01 (subtitles), +1 more"
	if got != want {
		t.Fatalf("FormatAlsoProcessing() = %q, want %q", got, want)
	}
}
