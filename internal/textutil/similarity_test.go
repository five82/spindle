package textutil

import (
	"fmt"
	"math"
	"testing"
)

func TestCosineSimilarityNil(t *testing.T) {
	fp := NewFingerprint("hello world")
	for name, pair := range map[string][2]*Fingerprint{
		"both nil": {nil, nil},
		"a nil":    {nil, fp},
		"b nil":    {fp, nil},
	} {
		if got := CosineSimilarity(pair[0], pair[1]); got != 0 {
			t.Errorf("%s: CosineSimilarity() = %v, want 0", name, got)
		}
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

func TestNewFingerprintReturnsNil(t *testing.T) {
	for name, text := range map[string]string{
		"empty":        "",
		"short tokens": "a an it to",
	} {
		if fp := NewFingerprint(text); fp != nil {
			t.Errorf("%s: expected nil fingerprint", name)
		}
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

func TestCorpusIDF(t *testing.T) {
	t.Run("common term gets near-zero IDF", func(t *testing.T) {
		c := NewCorpus()
		// Add 5 docs that all contain "batman"
		for i := range 5 {
			fp := NewFingerprint(fmt.Sprintf("batman episode %d content unique%d", i, i))
			c.Add(fp)
		}
		idf := c.IDF()
		// "batman" appears in all 5 docs → IDF = log(6/6) = 0
		batmanIDF := idf["batman"]
		if batmanIDF > 0.2 {
			t.Errorf("common term 'batman' IDF = %f, want near 0", batmanIDF)
		}
	})

	t.Run("rare term gets high IDF", func(t *testing.T) {
		c := NewCorpus()
		c.Add(NewFingerprint("batman riddler puzzle clue mystery"))
		c.Add(NewFingerprint("batman joker chaos destruction mayhem"))
		c.Add(NewFingerprint("batman penguin umbrella hideout scheme"))
		c.Add(NewFingerprint("batman catwoman diamond heist jewels"))
		idf := c.IDF()
		// "riddler" appears in 1/4 docs → IDF = log(5/2) ≈ 0.92
		riddlerIDF := idf["riddler"]
		batmanIDF := idf["batman"]
		if riddlerIDF <= batmanIDF {
			t.Errorf("rare term IDF (%f) should exceed common term IDF (%f)", riddlerIDF, batmanIDF)
		}
		if riddlerIDF < 0.5 {
			t.Errorf("rare term 'riddler' IDF = %f, want > 0.5", riddlerIDF)
		}
	})

	t.Run("empty corpus returns nil", func(t *testing.T) {
		c := NewCorpus()
		if idf := c.IDF(); idf != nil {
			t.Errorf("expected nil IDF for empty corpus, got %v", idf)
		}
	})

	t.Run("nil corpus returns nil", func(t *testing.T) {
		var c *Corpus
		if idf := c.IDF(); idf != nil {
			t.Errorf("expected nil IDF for nil corpus, got %v", idf)
		}
	})
}

func TestWithIDF(t *testing.T) {
	t.Run("reweights correctly", func(t *testing.T) {
		fp := NewFingerprint("batman robin gotham riddler")
		idf := map[string]float64{
			"batman":  0.1, // common
			"robin":   0.1, // common
			"gotham":  0.1, // common
			"riddler": 1.5, // rare
		}
		weighted := fp.WithIDF(idf)
		if weighted == nil {
			t.Fatal("expected non-nil weighted fingerprint")
		}
		if weighted.TokenCount() != 4 {
			t.Fatalf("expected 4 tokens, got %d", weighted.TokenCount())
		}
	})

	t.Run("nil fingerprint returns nil", func(t *testing.T) {
		var fp *Fingerprint
		got := fp.WithIDF(map[string]float64{"test": 1.0})
		if got != nil {
			t.Errorf("expected nil for nil fingerprint, got %v", got)
		}
	})

	t.Run("nil IDF returns original", func(t *testing.T) {
		fp := NewFingerprint("hello world test")
		got := fp.WithIDF(nil)
		if got != fp {
			t.Error("expected original fingerprint returned for nil IDF")
		}
	})

	t.Run("zero-weight terms dropped", func(t *testing.T) {
		fp := NewFingerprint("batman robin riddler")
		idf := map[string]float64{
			"batman":  0.0, // zeroed out
			"robin":   0.0, // zeroed out
			"riddler": 1.5,
		}
		weighted := fp.WithIDF(idf)
		if weighted == nil {
			t.Fatal("expected non-nil weighted fingerprint")
		}
		if weighted.TokenCount() != 1 {
			t.Fatalf("expected 1 token after zero-weight drop, got %d", weighted.TokenCount())
		}
	})

	t.Run("all terms zeroed returns nil", func(t *testing.T) {
		fp := NewFingerprint("batman robin gotham")
		idf := map[string]float64{
			"batman": 0.0,
			"robin":  0.0,
			"gotham": 0.0,
		}
		weighted := fp.WithIDF(idf)
		if weighted != nil {
			t.Error("expected nil when all terms zeroed out")
		}
	})
}

func TestIDFSeparatesSameShowEpisodes(t *testing.T) {
	// Simulate Batman scenario: episodes share common vocabulary
	// but have distinctive guest villain/plot terms.
	ep1 := NewFingerprint("batman robin gotham city riddler puzzle clue mystery crime")
	ep2 := NewFingerprint("batman robin gotham city joker chaos laugh destruction mayhem")
	ep3 := NewFingerprint("batman robin gotham city penguin umbrella hideout lair scheme")

	// Build corpus from all reference episodes
	corpus := NewCorpus()
	corpus.Add(ep1)
	corpus.Add(ep2)
	corpus.Add(ep3)
	idf := corpus.IDF()

	// Without IDF, ep1 vs ep2 would be quite similar due to shared vocabulary
	rawSim := CosineSimilarity(ep1, ep2)

	// With IDF, the shared vocabulary is downweighted
	ep1w := ep1.WithIDF(idf)
	ep2w := ep2.WithIDF(idf)
	idfSim := CosineSimilarity(ep1w, ep2w)

	// IDF-weighted similarity between different episodes should be lower
	if idfSim >= rawSim {
		t.Errorf("IDF-weighted cross-episode similarity (%f) should be lower than raw (%f)", idfSim, rawSim)
	}

	// Same episode with IDF should still be 1.0
	selfSim := CosineSimilarity(ep1w, ep1w)
	if selfSim < 0.99 {
		t.Errorf("IDF-weighted self-similarity = %f, want ~1.0", selfSim)
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
