// Package identification matches discs or dropped files to TMDB titles and
// enriches queue items before downstream stages run.
//
// The Identifier orchestrates MakeMKV scans, disc-ID cache lookups (to skip
// re-identification of previously seen discs), TMDB searches with runtime/year
// hints, and duplicate fingerprint detection. It records confidence scores,
// toggles review flows when metadata is uncertain, and emits notifications so
// users know when manual intervention is required.
//
// Centralize new identification heuristics here, keeping IO and queue updates in
// one place to avoid skew across stages.
package identification
