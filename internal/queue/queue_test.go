package queue

import (
	"context"
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

func TestUpdateAndVerify(t *testing.T) {
	store := openTestStore(t)

	item, err := store.NewDisc("Original", "fp1")
	if err != nil {
		t.Fatalf("new disc: %v", err)
	}

	item.Stage = StageEncoding
	item.InProgress = 1
	item.DiscTitle = "Updated Title"
	item.ErrorMessage = "some error"
	if err := store.Update(item); err != nil {
		t.Fatalf("update: %v", err)
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
	if got.ErrorMessage != "some error" {
		t.Errorf("error_message = %q, want %q", got.ErrorMessage, "some error")
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
	item2.Stage = StageEncoding
	_ = store.Update(item2)

	// All items.
	all, err := store.List()
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("list all = %d items, want 2", len(all))
	}

	// Filter by pending.
	pending, err := store.List(StageIdentification)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("list pending = %d items, want 1", len(pending))
	}
	if pending[0].ID != item1.ID {
		t.Errorf("pending item id = %d, want %d", pending[0].ID, item1.ID)
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
	item1.InProgress = 1
	_ = store.Update(item1)

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
	item3.Stage = StageEncoding
	_ = store.Update(item3)

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats[StageIdentification] != 2 {
		t.Errorf("pending = %d, want 2", stats[StageIdentification])
	}
	if stats[StageEncoding] != 1 {
		t.Errorf("encoding = %d, want 1", stats[StageEncoding])
	}
}

func TestResetInProgress(t *testing.T) {
	store := openTestStore(t)

	item, _ := store.NewDisc("A", "fp1")
	item.InProgress = 1
	_ = store.Update(item)

	if err := store.ResetInProgress(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	got, _ := store.GetByID(item.ID)
	if got.InProgress != 0 {
		t.Errorf("in_progress = %d, want 0", got.InProgress)
	}
}

func TestRetryFailedRouting(t *testing.T) {
	store := openTestStore(t)

	// Item with failed_at_stage set.
	item1, _ := store.NewDisc("A", "fp1")
	item1.Stage = StageFailed
	item1.FailedAtStage = string(StageEncoding)
	item1.ErrorMessage = "encode error"
	_ = store.Update(item1)

	// Item without failed_at_stage.
	item2, _ := store.NewDisc("B", "fp2")
	item2.Stage = StageFailed
	_ = store.Update(item2)

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
	item.Stage = StageEncoding
	item.InProgress = 1
	_ = store.Update(item)

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

	var reasons []string
	if err := json.Unmarshal([]byte(got.ReviewReason), &reasons); err != nil {
		t.Fatalf("unmarshal review_reason: %v", err)
	}
	found := false
	for _, r := range reasons {
		if r == "Stop requested by user" {
			found = true
		}
	}
	if !found {
		t.Errorf("review_reason %q does not contain 'Stop requested by user'", got.ReviewReason)
	}

	// Test stop-review override: try to update stage away from failed.
	got.Stage = StageEncoding
	if err := store.Update(got); err != nil {
		t.Fatalf("update after stop: %v", err)
	}

	overridden, _ := store.GetByID(item.ID)
	if overridden.Stage != StageFailed {
		t.Errorf("override: stage = %q, want %q (stop-review override)", overridden.Stage, StageFailed)
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

func TestMetadataIsMovie(t *testing.T) {
	tests := []struct {
		mediaType string
		movieBool bool
		want      bool
	}{
		{"movie", false, true},
		{"film", false, true},
		{"tv", true, false},
		{"tv_show", true, false},
		{"television", false, false},
		{"series", false, false},
		{"", true, true},
		{"", false, false},
		{"unknown_type", true, true},
	}

	for _, tt := range tests {
		m := Metadata{MediaType: tt.mediaType, Movie: tt.movieBool}
		got := m.IsMovie()
		if got != tt.want {
			t.Errorf("IsMovie(mediaType=%q, movie=%v) = %v, want %v",
				tt.mediaType, tt.movieBool, got, tt.want)
		}
	}
}

func TestGetFilenameMovie(t *testing.T) {
	m := Metadata{
		Title:     "The Matrix",
		MediaType: "movie",
		Year:      "1999",
	}
	got := m.GetFilename()
	want := "The Matrix (1999)"
	if got != want {
		t.Errorf("GetFilename() = %q, want %q", got, want)
	}

	// With edition.
	m.Edition = "Director's Cut"
	got = m.GetFilename()
	want = "The Matrix (1999) - Director's Cut"
	if got != want {
		t.Errorf("GetFilename() with edition = %q, want %q", got, want)
	}
}

func TestGetFilenameTV(t *testing.T) {
	// No episodes.
	m := Metadata{
		ShowTitle:    "Breaking Bad",
		MediaType:    "tv",
		SeasonNumber: 1,
	}
	got := m.GetFilename()
	want := "Breaking Bad - Season 01"
	if got != want {
		t.Errorf("GetFilename() no eps = %q, want %q", got, want)
	}

	// Single episode.
	m.Episodes = []MetadataEpisode{{Season: 1, Episode: 3}}
	got = m.GetFilename()
	want = "Breaking Bad - S01E03"
	if got != want {
		t.Errorf("GetFilename() single = %q, want %q", got, want)
	}

	// Multi-episode.
	m.Episodes = []MetadataEpisode{
		{Season: 1, Episode: 3},
		{Season: 1, Episode: 4},
		{Season: 1, Episode: 5},
	}
	got = m.GetFilename()
	want = "Breaking Bad - S01E03-E05"
	if got != want {
		t.Errorf("GetFilename() multi = %q, want %q", got, want)
	}
}

func TestGetLibraryPathMovie(t *testing.T) {
	m := Metadata{
		Title:     "Inception",
		MediaType: "movie",
		Year:      "2010",
	}
	got, err := m.GetLibraryPath("/media", "movies", "tv")
	if err != nil {
		t.Fatalf("GetLibraryPath: %v", err)
	}
	want := "/media/movies/Inception (2010)"
	if got != want {
		t.Errorf("GetLibraryPath() = %q, want %q", got, want)
	}
}

func TestGetLibraryPathTV(t *testing.T) {
	m := Metadata{
		ShowTitle:    "The Office",
		MediaType:    "tv",
		SeasonNumber: 3,
	}
	got, err := m.GetLibraryPath("/media", "movies", "tv")
	if err != nil {
		t.Fatalf("GetLibraryPath: %v", err)
	}
	want := "/media/tv/The Office/Season 03"
	if got != want {
		t.Errorf("GetLibraryPath() = %q, want %q", got, want)
	}
}

func TestMetadataFromJSON(t *testing.T) {
	data := `{"title":"Test Movie","media_type":"movie","year":"2020"}`
	m := MetadataFromJSON(data, "Fallback")
	if m.Title != "Test Movie" {
		t.Errorf("title = %q, want %q", m.Title, "Test Movie")
	}
	if m.Year != "2020" {
		t.Errorf("year = %q, want %q", m.Year, "2020")
	}

	// Empty data uses fallback.
	m = MetadataFromJSON("", "Fallback Title")
	if m.Title != "Fallback Title" {
		t.Errorf("fallback title = %q, want %q", m.Title, "Fallback Title")
	}

	// Invalid JSON uses fallback.
	m = MetadataFromJSON("{invalid", "Fallback")
	if m.Title != "Fallback" {
		t.Errorf("invalid json title = %q, want %q", m.Title, "Fallback")
	}
}

func TestCheckHealth(t *testing.T) {
	store := openTestStore(t)
	if err := store.CheckHealth(); err != nil {
		t.Fatalf("health check: %v", err)
	}
}

func TestClearAndClearCompleted(t *testing.T) {
	store := openTestStore(t)

	item1, _ := store.NewDisc("A", "fp1")
	item2, _ := store.NewDisc("B", "fp2")
	item1.Stage = StageCompleted
	_ = store.Update(item1)

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
	item.Stage = StageIdentification
	item.InProgress = 1
	_ = store.Update(item)

	has, err = store.HasDiscDependentItem()
	if err != nil {
		t.Fatalf("has disc dependent: %v", err)
	}
	if !has {
		t.Error("expected true with identification in progress")
	}
}

// stubEncoder implements RipSpecEncoder for testing.
type stubEncoder struct {
	data string
	err  error
}

func (s *stubEncoder) Encode() (string, error) {
	return s.data, s.err
}

func TestPersistRipSpec(t *testing.T) {
	store := openTestStore(t)
	item, _ := store.NewDisc("Test", "fp1")

	enc := &stubEncoder{data: `{"tracks":[1,2]}`}
	if err := PersistRipSpec(context.Background(), store, item, enc); err != nil {
		t.Fatalf("persist: %v", err)
	}

	got, _ := store.GetByID(item.ID)
	if got.RipSpecData != `{"tracks":[1,2]}` {
		t.Errorf("rip_spec_data = %q, want %q", got.RipSpecData, `{"tracks":[1,2]}`)
	}
}
