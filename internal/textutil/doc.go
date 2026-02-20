// Package textutil provides text processing utilities for fingerprinting, similarity,
// and filename sanitization.
//
// The primary use cases are:
//   - Creating token-based fingerprints from text for comparison
//   - Computing cosine similarity between fingerprints
//   - Sanitizing filenames and path segments for safe filesystem use
//
// Fingerprints use term frequency vectors normalized for efficient comparison.
// The tokenization process lowercases text, splits on non-alphanumeric characters,
// and filters tokens shorter than 3 characters.
package textutil
