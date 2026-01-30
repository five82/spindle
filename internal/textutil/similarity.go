package textutil

// CosineSimilarity computes the cosine similarity between two fingerprints.
// Returns 0 if either fingerprint is nil or has zero norm.
func CosineSimilarity(a, b *Fingerprint) float64 {
	if a == nil || b == nil || a.norm == 0 || b.norm == 0 {
		return 0
	}
	var dot float64
	for token, count := range a.tokens {
		if other, ok := b.tokens[token]; ok {
			dot += count * other
		}
	}
	if dot == 0 {
		return 0
	}
	return dot / (a.norm * b.norm)
}
