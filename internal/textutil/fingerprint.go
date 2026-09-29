package textutil

import "math"

// Fingerprint is a term-frequency vector with an L2 norm.
type Fingerprint struct {
	Terms map[string]float64
	Norm  float64
}

// NewFingerprint creates an L2-normalized TF vector from text.
// Returns nil if no valid tokens are produced.
func NewFingerprint(text string) *Fingerprint {
	tokens := Tokenize(text)
	if len(tokens) == 0 {
		return nil
	}
	terms := make(map[string]float64, len(tokens))
	for _, t := range tokens {
		terms[t]++
	}
	fp := &Fingerprint{Terms: terms}
	fp.normalize()
	return fp
}

func (f *Fingerprint) normalize() {
	var sum float64
	for _, v := range f.Terms {
		sum += v * v
	}
	f.Norm = math.Sqrt(sum)
	if f.Norm > 0 {
		for k := range f.Terms {
			f.Terms[k] /= f.Norm
		}
		f.Norm = 1.0
	}
}

// CosineSimilarity computes the cosine similarity between two fingerprints.
// Returns 0 if either fingerprint is nil or has a zero norm.
func CosineSimilarity(a, b *Fingerprint) float64 {
	if a == nil || b == nil || a.Norm == 0 || b.Norm == 0 {
		return 0
	}
	var dot float64
	for k, va := range a.Terms {
		if vb, ok := b.Terms[k]; ok {
			dot += va * vb
		}
	}
	return dot / (a.Norm * b.Norm)
}
