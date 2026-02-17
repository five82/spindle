// Package tmdb provides the minimal TMDB API client used during disc
// identification.
//
// It authenticates requests and exposes movie, TV, and multi search with optional
// release-year and runtime filters, season/episode detail lookups, and movie/TV
// detail retrieval. Responses are strongly typed so the identification stage can
// score them. Options allow tests to supply custom HTTP clients or stub behaviour
// without modifying production code.
package tmdb
