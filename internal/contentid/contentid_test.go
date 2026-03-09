package contentid

import (
	"math"
	"os"
	"path/filepath"
	"testing"

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
