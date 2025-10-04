// Package tmdb provides the minimal TMDB API client used during disc
// identification.
//
// It authenticates requests, exposes movie search with optional release-year and
// runtime filters, and returns strongly typed responses that the identification
// stage can score. Options allow tests to supply custom HTTP clients or stub
// behaviour without modifying production code.
package tmdb
