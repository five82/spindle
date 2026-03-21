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

// WithIDF applies TF-IDF weights and returns a new fingerprint.
// Terms absent from the IDF map retain their original weight.
// Zero-weight terms are dropped. Returns nil if all terms are zeroed.
func (f *Fingerprint) WithIDF(idf map[string]float64) *Fingerprint {
	if f == nil {
		return nil
	}
	terms := make(map[string]float64, len(f.Terms))
	for k, v := range f.Terms {
		weight, ok := idf[k]
		if !ok {
			terms[k] = v
			continue
		}
		w := v * weight
		if w != 0 {
			terms[k] = w
		}
	}
	if len(terms) == 0 {
		return nil
	}
	fp := &Fingerprint{Terms: terms}
	fp.normalize()
	return fp
}

// Corpus tracks document frequency across fingerprints for IDF computation.
type Corpus struct {
	docFreq map[string]int
	numDocs int
}

// Add registers the unique terms in a fingerprint, incrementing their document count.
func (c *Corpus) Add(fp *Fingerprint) {
	if fp == nil {
		return
	}
	if c.docFreq == nil {
		c.docFreq = make(map[string]int)
	}
	c.numDocs++
	for k := range fp.Terms {
		c.docFreq[k]++
	}
}

// IDF computes inverse document frequency weights as log((N+1)/(1+df)) for each term.
func (c *Corpus) IDF() map[string]float64 {
	if c.docFreq == nil {
		return nil
	}
	idf := make(map[string]float64, len(c.docFreq))
	n := float64(c.numDocs)
	for term, df := range c.docFreq {
		idf[term] = math.Log((n + 1) / (1 + float64(df)))
	}
	return idf
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
