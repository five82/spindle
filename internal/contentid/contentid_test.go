package contentid

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/textutil"
)

// ---------------------------------------------------------------------------
// hungarian
// ---------------------------------------------------------------------------

func TestHungarianIdentityMatrix(t *testing.T) {
	// Identity-like matrix: each row has 1.0 on the diagonal.
	scores := [][]float64{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
	}
	assignments := hungarian(scores)
	if len(assignments) != 3 {
		t.Fatalf("expected 3 assignments, got %d", len(assignments))
	}
	for i, col := range assignments {
		if col != i {
			t.Errorf("row %d: expected col %d, got %d", i, i, col)
		}
	}
}

func TestHungarianSmall3x3(t *testing.T) {
	// Row 0 best match is col 2 (0.95)
	// Row 1 best match is col 0 (0.90)
	// Row 2 best match is col 1 (0.88)
	scores := [][]float64{
		{0.60, 0.70, 0.95},
		{0.90, 0.65, 0.60},
		{0.59, 0.88, 0.62},
	}
	assignments := hungarian(scores)
	if len(assignments) != 3 {
		t.Fatalf("expected 3 assignments, got %d", len(assignments))
	}
	want := []int{2, 0, 1}
	for i, col := range assignments {
		if col != want[i] {
			t.Errorf("row %d: expected col %d, got %d", i, want[i], col)
		}
	}
}

func TestHungarianBelowThreshold(t *testing.T) {
	// All scores below minSimilarityScore (0.58) -- none should be assigned.
	scores := [][]float64{
		{0.30, 0.40},
		{0.50, 0.57},
	}
	assignments := hungarian(scores)
	if len(assignments) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(assignments))
	}
	for i, col := range assignments {
		if col != -1 {
			t.Errorf("row %d: expected -1 (unassigned), got %d", i, col)
		}
	}
}

func TestHungarianPartialThreshold(t *testing.T) {
	// Row 0 has a match above threshold, row 1 does not.
	scores := [][]float64{
		{0.90, 0.30},
		{0.40, 0.50},
	}
	assignments := hungarian(scores)
	if assignments[0] != 0 {
		t.Errorf("row 0: expected col 0, got %d", assignments[0])
	}
	if assignments[1] != -1 {
		t.Errorf("row 1: expected -1 (unassigned), got %d", assignments[1])
	}
}

func TestHungarianEmpty(t *testing.T) {
	assignments := hungarian(nil)
	if assignments != nil {
		t.Errorf("expected nil for empty matrix, got %v", assignments)
	}
}

// ---------------------------------------------------------------------------
// checkContiguity
// ---------------------------------------------------------------------------

func TestCheckContiguityContiguous(t *testing.T) {
	matches := []Match{
		{DiscKey: "a", EpisodeNum: 3, Score: 0.9},
		{DiscKey: "b", EpisodeNum: 4, Score: 0.9},
		{DiscKey: "c", EpisodeNum: 5, Score: 0.9},
	}
	if !checkContiguity(matches) {
		t.Error("expected contiguous for [3,4,5]")
	}
}

func TestCheckContiguityNonContiguous(t *testing.T) {
	matches := []Match{
		{DiscKey: "a", EpisodeNum: 1, Score: 0.9},
		{DiscKey: "b", EpisodeNum: 3, Score: 0.9},
		{DiscKey: "c", EpisodeNum: 5, Score: 0.9},
	}
	if checkContiguity(matches) {
		t.Error("expected non-contiguous for [1,3,5]")
	}
}

func TestCheckContiguitySingle(t *testing.T) {
	matches := []Match{
		{DiscKey: "a", EpisodeNum: 7, Score: 0.9},
	}
	if !checkContiguity(matches) {
		t.Error("expected contiguous for single match")
	}
}

func TestCheckContiguityEmpty(t *testing.T) {
	if !checkContiguity(nil) {
		t.Error("expected contiguous for empty matches")
	}
}

func TestCheckContiguityUnsorted(t *testing.T) {
	// Out of order but contiguous once sorted.
	matches := []Match{
		{DiscKey: "c", EpisodeNum: 5, Score: 0.9},
		{DiscKey: "a", EpisodeNum: 3, Score: 0.9},
		{DiscKey: "b", EpisodeNum: 4, Score: 0.9},
	}
	if !checkContiguity(matches) {
		t.Error("expected contiguous for [5,3,4] (sorts to [3,4,5])")
	}
}

// ---------------------------------------------------------------------------
// readSRTText
// ---------------------------------------------------------------------------

func TestReadSRTText(t *testing.T) {
	content := `1
00:00:01,000 --> 00:00:03,000
Hello world.

2
00:00:04,000 --> 00:00:06,000
This is a test.

3
00:00:07,000 --> 00:00:09,000
Goodbye world.
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.srt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got := readSRTText(path)
	want := "Hello world. This is a test. Goodbye world."
	if got != want {
		t.Errorf("readSRTText:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestReadSRTTextEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.srt")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	got := readSRTText(path)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestReadSRTTextMissing(t *testing.T) {
	got := readSRTText("/nonexistent/path.srt")
	if got != "" {
		t.Errorf("expected empty string for missing file, got %q", got)
	}
}

func TestApplyMatchesRemapsAssetKeys(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{}
	item := &queue.Item{}
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s03_001", Season: 3},
			{Key: "s03_002", Season: 3},
		},
		Assets: ripspec.Assets{
			Ripped: []ripspec.Asset{
				{EpisodeKey: "s03_001", Path: "/rip/1.mkv", Status: "completed"},
				{EpisodeKey: "s03_002", Path: "/rip/2.mkv", Status: "completed"},
			},
		},
	}

	h.applyMatches(logger, env, []Match{
		{DiscKey: "s03_001", EpisodeNum: 3, Score: 0.91},
		{DiscKey: "s03_002", EpisodeNum: 4, Score: 0.88},
	}, item)

	if env.Episodes[0].Key != "s03e03" || env.Episodes[1].Key != "s03e04" {
		t.Fatalf("episode keys not remapped: %+v", env.Episodes)
	}
	if _, ok := env.Assets.FindAsset("ripped", "s03e03"); !ok {
		t.Fatal("ripped asset for s03e03 not found after applyMatches remap")
	}
	if _, ok := env.Assets.FindAsset("ripped", "s03e04"); !ok {
		t.Fatal("ripped asset for s03e04 not found after applyMatches remap")
	}
	if _, ok := env.Assets.FindAsset("ripped", "s03_001"); ok {
		t.Fatal("old ripped key s03_001 still found after applyMatches remap")
	}
}

func TestApplyMatchesFlagsEpisodeLevelReview(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{}
	item := &queue.Item{}
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{{Key: "s03_001", Season: 3}},
	}

	h.applyMatches(logger, env, []Match{{DiscKey: "s03_001", EpisodeNum: 1, Score: 0.63}}, item)

	if !env.Episodes[0].NeedsReview {
		t.Fatal("episode NeedsReview = false, want true")
	}
	if env.Episodes[0].ReviewReason == "" {
		t.Fatal("episode ReviewReason = empty, want populated")
	}
	if item.NeedsReview != 1 {
		t.Fatalf("item.NeedsReview = %d, want 1", item.NeedsReview)
	}
}

func TestBuildContentIDSummary(t *testing.T) {
	env := &ripspec.Envelope{Episodes: []ripspec.Episode{
		{Key: "s01e01", Episode: 1, MatchConfidence: 0.91},
		{Key: "s01e02", Episode: 2, MatchConfidence: 0.64, NeedsReview: true},
		{Key: "s01_003", Episode: 0, NeedsReview: true},
	}}
	summary := buildContentIDSummary(env, []Match{{DiscKey: "s01_001", EpisodeNum: 1, Score: 0.91}, {DiscKey: "s01_002", EpisodeNum: 2, Score: 0.64}}, 3, 4)
	if summary == nil {
		t.Fatal("summary = nil")
	}
	if summary.Method != "whisperx_tfidf_hungarian" {
		t.Fatalf("Method = %q", summary.Method)
	}
	if summary.ReferenceSource != "opensubtitles" {
		t.Fatalf("ReferenceSource = %q", summary.ReferenceSource)
	}
	if summary.MatchedEpisodes != 2 || summary.UnresolvedEpisodes != 1 {
		t.Fatalf("matched/unresolved = %d/%d", summary.MatchedEpisodes, summary.UnresolvedEpisodes)
	}
	if summary.LowConfidenceCount != 1 {
		t.Fatalf("LowConfidenceCount = %d, want 1", summary.LowConfidenceCount)
	}
	if !summary.Completed || !summary.EpisodesSynchronized {
		t.Fatal("summary completion flags not set")
	}
}

// ---------------------------------------------------------------------------
// isDigitsOnly
// ---------------------------------------------------------------------------

func TestIsDigitsOnly(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"123", true},
		{"0", true},
		{"12a3", false},
		{"", false},
		{"abc", false},
	}
	for _, tt := range tests {
		if got := isDigitsOnly(tt.input); got != tt.want {
			t.Errorf("isDigitsOnly(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Fingerprint + IDF + cosine similarity integration
// ---------------------------------------------------------------------------

func TestDownloadReferencesReturnsErrorOnOperationalFailure(t *testing.T) {
	var downloadCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/subtitles":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"id": "1",
					"attributes": map[string]any{
						"language":           "en",
						"download_count":     10,
						"hearing_impaired":   false,
						"foreign_parts_only": false,
						"files":              []map[string]any{{"file_id": 123, "file_name": "test.srt"}},
					},
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/download":
			downloadCalls++
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"message":"service unavailable"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := opensubtitles.New("test-key", "TestAgent", "token", srv.URL, nil)
	h := &Handler{osClient: client}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{ID: 2287, SeasonNumber: 3},
		Episodes: []ripspec.Episode{{Key: "s03_001"}},
	}

	refs, err := h.downloadReferences(context.Background(), logger, env)
	if err == nil {
		t.Fatal("expected operational OpenSubtitles failure to be returned")
	}
	if refs != nil {
		t.Fatalf("expected nil refs on operational failure, got %#v", refs)
	}
	if downloadCalls != 4 {
		t.Fatalf("expected 4 download attempts (initial + retries), got %d", downloadCalls)
	}
}

func TestDownloadReferencesAllowsNoResultsWithoutError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/subtitles" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	client := opensubtitles.New("test-key", "TestAgent", "token", srv.URL, nil)
	h := &Handler{osClient: client}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{ID: 2287, SeasonNumber: 3},
		Episodes: []ripspec.Episode{{Key: "s03_001"}},
	}

	refs, err := h.downloadReferences(context.Background(), logger, env)
	if err != nil {
		t.Fatalf("expected no error for empty search results, got %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected no refs, got %d", len(refs))
	}
}

func TestFingerprintIDFIntegration(t *testing.T) {
	// Two similar documents and one different.
	docA := textutil.NewFingerprint("the quick brown fox jumps over the lazy dog")
	docB := textutil.NewFingerprint("the quick brown fox leaps over the lazy cat")
	docC := textutil.NewFingerprint("completely different text about submarines and oceans")

	if docA == nil || docB == nil || docC == nil {
		t.Fatal("expected non-nil fingerprints")
	}

	// Build corpus and compute IDF.
	corpus := &textutil.Corpus{}
	corpus.Add(docA)
	corpus.Add(docB)
	corpus.Add(docC)
	idf := corpus.IDF()

	// Apply IDF weights.
	wA := docA.WithIDF(idf)
	wB := docB.WithIDF(idf)
	wC := docC.WithIDF(idf)

	if wA == nil || wB == nil || wC == nil {
		t.Fatal("expected non-nil weighted fingerprints")
	}

	// A and B should be more similar than A and C.
	simAB := textutil.CosineSimilarity(wA, wB)
	simAC := textutil.CosineSimilarity(wA, wC)

	if simAB <= simAC {
		t.Errorf("expected sim(A,B) > sim(A,C), got %.4f <= %.4f", simAB, simAC)
	}

	// A-B similarity should be positive.
	if simAB <= 0 {
		t.Errorf("expected positive similarity between A and B, got %.4f", simAB)
	}

	// A-C similarity should be near zero (no shared tokens).
	if math.Abs(simAC) > 0.01 {
		t.Errorf("expected near-zero similarity between A and C, got %.4f", simAC)
	}
}
