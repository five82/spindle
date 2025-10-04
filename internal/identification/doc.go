// Package identification matches discs or dropped files to TMDB titles and
// enriches queue items before downstream stages run.
//
// The Identifier orchestrates MakeMKV scans, fingerprint lookups, TMDB searches
// with runtime/year hints, and duplicate detection. It records confidence scores,
// toggles review flows when metadata is uncertain, and emits notifications so
// users know when manual intervention is required.
//
// Centralize new identification heuristics here, keeping IO and queue updates in
// one place to avoid skew across stages.
package identification
