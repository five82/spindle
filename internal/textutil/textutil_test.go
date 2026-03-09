package textutil

import (
	"math"
	"testing"
)

// ---------------------------------------------------------------------------
// Tokenize
// ---------------------------------------------------------------------------

func TestTokenize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty string", "", nil},
		{"only short tokens", "a bb", nil},
		{"special chars", "foo--bar!!baz", []string{"foo", "bar", "baz"}},
		{"mixed case", "Hello World", []string{"hello", "world"}},
		{"numbers kept", "abc123 def", []string{"abc123", "def"}},
		{"short tokens filtered", "go is fun today", []string{"fun", "today"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tokenize(tt.input)
			if !strSliceEqual(got, tt.want) {
				t.Errorf("Tokenize(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Fingerprint
// ---------------------------------------------------------------------------

func TestNewFingerprint(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
	}{
		{"empty text", "", true},
		{"only short tokens", "a bb", true},
		{"valid text", "hello world hello", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := NewFingerprint(tt.input)
			if tt.wantNil && fp != nil {
				t.Errorf("expected nil fingerprint for %q", tt.input)
			}
			if !tt.wantNil && fp == nil {
				t.Errorf("expected non-nil fingerprint for %q", tt.input)
			}
		})
	}
}

func TestFingerprintNormalized(t *testing.T) {
	fp := NewFingerprint("hello world hello")
	if fp == nil {
		t.Fatal("expected non-nil fingerprint")
	}
	if math.Abs(fp.Norm-1.0) > 1e-9 {
		t.Errorf("expected norm 1.0, got %f", fp.Norm)
	}
}

// ---------------------------------------------------------------------------
// CosineSimilarity
// ---------------------------------------------------------------------------

func TestCosineSimilarity(t *testing.T) {
	t.Run("identical texts", func(t *testing.T) {
		a := NewFingerprint("hello world testing")
		b := NewFingerprint("hello world testing")
		sim := CosineSimilarity(a, b)
		if math.Abs(sim-1.0) > 1e-9 {
			t.Errorf("identical texts: got %f, want 1.0", sim)
		}
	})

	t.Run("orthogonal texts", func(t *testing.T) {
		a := NewFingerprint("alpha bravo charlie")
		b := NewFingerprint("delta echo foxtrot")
		sim := CosineSimilarity(a, b)
		if math.Abs(sim) > 1e-9 {
			t.Errorf("orthogonal texts: got %f, want 0.0", sim)
		}
	})

	t.Run("known angle", func(t *testing.T) {
		a := NewFingerprint("hello world testing")
		b := NewFingerprint("hello world different")
		sim := CosineSimilarity(a, b)
		if sim <= 0 || sim >= 1.0 {
			t.Errorf("partial overlap: got %f, want between 0 and 1", sim)
		}
	})

	t.Run("nil fingerprint", func(t *testing.T) {
		a := NewFingerprint("hello world testing")
		sim := CosineSimilarity(a, nil)
		if sim != 0 {
			t.Errorf("nil fingerprint: got %f, want 0.0", sim)
		}
	})

	t.Run("both nil", func(t *testing.T) {
		sim := CosineSimilarity(nil, nil)
		if sim != 0 {
			t.Errorf("both nil: got %f, want 0.0", sim)
		}
	})
}

// ---------------------------------------------------------------------------
// Corpus / IDF
// ---------------------------------------------------------------------------

func TestCorpusIDF(t *testing.T) {
	var c Corpus
	c.Add(NewFingerprint("hello world testing"))
	c.Add(NewFingerprint("hello world different"))
	c.Add(NewFingerprint("alpha bravo charlie"))

	idf := c.IDF()
	if idf == nil {
		t.Fatal("expected non-nil IDF map")
	}

	// "hello" appears in 2 of 3 docs: log((3+1)/(1+2)) = log(4/3)
	wantHello := math.Log(4.0 / 3.0)
	if math.Abs(idf["hello"]-wantHello) > 1e-9 {
		t.Errorf("idf[hello] = %f, want %f", idf["hello"], wantHello)
	}

	// "alpha" appears in 1 of 3 docs: log((3+1)/(1+1)) = log(2)
	wantAlpha := math.Log(4.0 / 2.0)
	if math.Abs(idf["alpha"]-wantAlpha) > 1e-9 {
		t.Errorf("idf[alpha] = %f, want %f", idf["alpha"], wantAlpha)
	}
}

func TestCorpusAddNil(t *testing.T) {
	var c Corpus
	c.Add(nil) // should not panic
	idf := c.IDF()
	if idf != nil {
		t.Errorf("expected nil IDF for empty corpus, got %v", idf)
	}
}

// ---------------------------------------------------------------------------
// WithIDF
// ---------------------------------------------------------------------------

func TestWithIDF(t *testing.T) {
	fp := NewFingerprint("hello world testing")

	t.Run("absent terms retain weight", func(t *testing.T) {
		idf := map[string]float64{"hello": 2.0}
		result := fp.WithIDF(idf)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if _, ok := result.Terms["world"]; !ok {
			t.Error("expected 'world' to be retained")
		}
	})

	t.Run("zero weight drops term", func(t *testing.T) {
		idf := map[string]float64{
			"hello":   0.0,
			"world":   0.0,
			"testing": 0.0,
		}
		result := fp.WithIDF(idf)
		if result != nil {
			t.Errorf("expected nil when all terms zeroed, got %v", result)
		}
	})

	t.Run("nil receiver", func(t *testing.T) {
		var nilFP *Fingerprint
		result := nilFP.WithIDF(map[string]float64{"hello": 1.0})
		if result != nil {
			t.Error("expected nil from nil receiver")
		}
	})
}

// ---------------------------------------------------------------------------
// SanitizeDisplayName
// ---------------------------------------------------------------------------

func TestSanitizeDisplayName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"colons and slashes", "Movie: Part/One\\Two", "Movie Part One Two"},
		{"special chars removed", `A?"<>|*B`, "AB"},
		{"control chars", "hello\x00world\x1ftest", "hello world test"},
		{"whitespace collapse", "hello   world", "hello world"},
		{"empty fallback", "", "manual-import"},
		{"only special chars", `?"<>|*`, "manual-import"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeDisplayName(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeDisplayName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SanitizePathSegment
// ---------------------------------------------------------------------------

func TestSanitizePathSegment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"slashes to dashes", "a/b\\c:d*e", "a-b-c-d-e"},
		{"special chars removed", `a?"<>|b`, "ab"},
		{"spaces to hyphens", "hello world", "hello-world"},
		{"trim hyphens", "-hello-", "hello"},
		{"trim underscores", "_hello_", "hello"},
		{"empty fallback", "", "queue"},
		{"only special chars", `?"<>|`, "queue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizePathSegment(tt.input)
			if got != tt.want {
				t.Errorf("SanitizePathSegment(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SanitizeToken
// ---------------------------------------------------------------------------

func TestSanitizeToken(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"lowercase", "Hello", "hello"},
		{"special chars to underscore", "a.b/c", "a_b_c"},
		{"keeps dashes and underscores", "foo-bar_baz", "foo-bar_baz"},
		{"empty fallback", "", "unknown"},
		{"only special chars", "!!!", "unknown"},
		{"spaces to underscore", "hello world", "hello_world"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeToken(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeToken(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SafeJoin
// ---------------------------------------------------------------------------

func TestSafeJoin(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		segment string
		wantErr bool
	}{
		{"simple join", "/tmp/base", "sub/file.txt", false},
		{"dot-dot escape", "/tmp/base", "../etc/passwd", true},
		{"absolute segment", "/tmp/base", "/etc/passwd", true},
		{"dot segment", "/tmp/base", ".", false},
		{"nested dot-dot", "/tmp/base", "a/../../etc", true},
		{"valid nested", "/tmp/base", "a/b/c", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SafeJoin(tt.base, tt.segment)
			if tt.wantErr && err == nil {
				t.Errorf("SafeJoin(%q, %q) = %q, expected error", tt.base, tt.segment, result)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("SafeJoin(%q, %q) returned error: %v", tt.base, tt.segment, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Ternary
// ---------------------------------------------------------------------------

func TestTernary(t *testing.T) {
	if got := Ternary(true, "yes", "no"); got != "yes" {
		t.Errorf("Ternary(true) = %q, want %q", got, "yes")
	}
	if got := Ternary(false, "yes", "no"); got != "no" {
		t.Errorf("Ternary(false) = %q, want %q", got, "no")
	}
	if got := Ternary(true, 1, 2); got != 1 {
		t.Errorf("Ternary(true, 1, 2) = %d, want 1", got)
	}
	if got := Ternary(false, 1, 2); got != 2 {
		t.Errorf("Ternary(false, 1, 2) = %d, want 2", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func strSliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
