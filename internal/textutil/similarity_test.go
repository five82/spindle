package textutil

import (
	"math"
	"testing"
)

func TestCosineSimilarityNil(t *testing.T) {
	tests := []struct {
		name string
		a    *Fingerprint
		b    *Fingerprint
		want float64
	}{
		{"both nil", nil, nil, 0},
		{"a nil", nil, NewFingerprint("hello world"), 0},
		{"b nil", NewFingerprint("hello world"), nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineSimilarity(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("CosineSimilarity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCosineSimilarityIdentical(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog"
	a := NewFingerprint(text)
	b := NewFingerprint(text)

	got := CosineSimilarity(a, b)
	if got != 1.0 {
		t.Errorf("CosineSimilarity(identical) = %v, want 1.0", got)
	}
}

func TestCosineSimilarityCompleteDifferent(t *testing.T) {
	a := NewFingerprint("apple banana cherry")
	b := NewFingerprint("dog elephant frog")

	got := CosineSimilarity(a, b)
	if got != 0 {
		t.Errorf("CosineSimilarity(different) = %v, want 0", got)
	}
}

func TestCosineSimilarityPartialOverlap(t *testing.T) {
	a := NewFingerprint("the quick brown fox")
	b := NewFingerprint("the slow brown cat")

	got := CosineSimilarity(a, b)
	if got <= 0 || got >= 1 {
		t.Errorf("CosineSimilarity(partial) = %v, want between 0 and 1", got)
	}
}

func TestCosineSimilaritySymmetric(t *testing.T) {
	a := NewFingerprint("hello world program")
	b := NewFingerprint("world program test")

	ab := CosineSimilarity(a, b)
	ba := CosineSimilarity(b, a)

	if ab != ba {
		t.Errorf("CosineSimilarity not symmetric: (%v, %v)", ab, ba)
	}
}

func TestCosineSimilarityZeroNorm(t *testing.T) {
	// Create fingerprint with zero norm (empty tokens)
	a := &Fingerprint{tokens: map[string]float64{}, norm: 0}
	b := NewFingerprint("hello world test")

	got := CosineSimilarity(a, b)
	if got != 0 {
		t.Errorf("CosineSimilarity(zero norm) = %v, want 0", got)
	}
}

func TestNewFingerprintEmpty(t *testing.T) {
	fp := NewFingerprint("")
	if fp != nil {
		t.Error("expected nil for empty text")
	}
}

func TestNewFingerprintShortTokens(t *testing.T) {
	// Only short tokens (< 3 chars) should result in nil
	fp := NewFingerprint("a an it to")
	if fp != nil {
		t.Error("expected nil for text with only short tokens")
	}
}

func TestNewFingerprintValid(t *testing.T) {
	fp := NewFingerprint("hello world programming")
	if fp == nil {
		t.Fatal("expected fingerprint, got nil")
	}
	if fp.norm == 0 {
		t.Error("expected non-zero norm")
	}
	if len(fp.tokens) == 0 {
		t.Error("expected tokens")
	}
}

func TestNewFingerprintNormCalculation(t *testing.T) {
	// "hello hello world" -> hello:2, world:1
	// norm = sqrt(2^2 + 1^2) = sqrt(5)
	fp := NewFingerprint("hello hello world")
	if fp == nil {
		t.Fatal("expected fingerprint")
	}

	expectedNorm := math.Sqrt(5)
	if math.Abs(fp.norm-expectedNorm) > 0.0001 {
		t.Errorf("norm = %v, want %v", fp.norm, expectedNorm)
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "simple words",
			input: "Hello World",
			want:  []string{"hello", "world"},
		},
		{
			name:  "filters short",
			input: "a to the quick fox",
			want:  []string{"the", "quick", "fox"},
		},
		{
			name:  "handles punctuation",
			input: "Hello, World! How are you?",
			want:  []string{"hello", "world", "how", "are", "you"},
		},
		{
			name:  "handles numbers",
			input: "test123 456test",
			want:  []string{"test123", "456test"},
		},
		{
			name:  "empty string",
			input: "",
			want:  []string{},
		},
		{
			name:  "only short tokens",
			input: "a b c",
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tokenize(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("Tokenize() = %v (len %d), want %v (len %d)",
					got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("token[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFingerprintTokenCount(t *testing.T) {
	tests := []struct {
		name string
		fp   *Fingerprint
		want int
	}{
		{
			name: "nil fingerprint",
			fp:   nil,
			want: 0,
		},
		{
			name: "unique tokens",
			fp:   NewFingerprint("hello world programming"),
			want: 3,
		},
		{
			name: "repeated tokens",
			fp:   NewFingerprint("hello hello world world world"),
			want: 2, // unique count
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fp.TokenCount()
			if got != tt.want {
				t.Errorf("TokenCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCosineSimilarityRealisticCommentary(t *testing.T) {
	// Simulate main audio transcript (movie dialogue)
	mainAudio := `
		The story begins with our hero arriving at the castle.
		He knew this day would come. The prophecy spoke of a chosen one.
		Together they would face the darkness that threatened the realm.
	`

	// Simulate stereo downmix (same content, should be similar)
	stereoDownmix := `
		The story begins with our hero arriving at the castle.
		He knew this day would come. The prophecy spoke of a chosen one.
		Together they would face the darkness that threatened the realm.
	`

	// Simulate commentary track (different content, discussing the film)
	commentary := `
		So we shot this scene in New Zealand actually.
		The director wanted a really specific look for the castle.
		I remember we had to do about fifteen takes of this.
		The lighting was tricky because of the weather.
	`

	mainFP := NewFingerprint(mainAudio)
	stereoFP := NewFingerprint(stereoDownmix)
	commentaryFP := NewFingerprint(commentary)

	// Stereo downmix should be very similar to main audio
	// Use approximate comparison due to floating point precision
	stereoSim := CosineSimilarity(mainFP, stereoFP)
	if stereoSim < 0.99 {
		t.Errorf("stereo downmix similarity = %v, want ~1.0", stereoSim)
	}

	// Commentary should be different from main audio
	commentarySim := CosineSimilarity(mainFP, commentaryFP)
	if commentarySim >= 0.5 {
		t.Errorf("commentary similarity = %v, should be < 0.5", commentarySim)
	}
}
