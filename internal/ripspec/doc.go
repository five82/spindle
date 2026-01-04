// Package ripspec defines the structured payload shared between workflow stages.
//
// The Envelope type captures disc metadata, title information, episode mappings,
// and realised artefacts as items progress through identification, ripping,
// encoding, subtitles, and organising. Stages read and extend the envelope
// rather than maintaining separate state, so the RipSpec becomes the single
// source of truth for what the disc contains and what has been produced.
//
// # Key Types
//
// Envelope: root container with Fingerprint, ContentKey, Metadata, Titles,
// Episodes, Assets, and Attributes. Persisted as JSON in queue.rip_spec_data.
//
// Title: playlist metadata from disc scanning (ID, duration, chapters, segments).
//
// Episode: target episode to be produced (season, episode, runtime, output basename).
//
// Assets: ripped/encoded/subtitled/final file paths keyed by episode.
//
// # Lifecycle
//
// Identification populates Fingerprint, ContentKey, Metadata, and Titles.
// Ripping adds Assets.Ripped entries. Episode identification fills Episodes
// and may update Attributes with content_id_matches. Encoding adds Assets.Encoded.
// Subtitles adds Assets.Subtitled and subtitle_generation_results to Attributes.
// Organizer adds Assets.Final and moves files to the library.
//
// # Entry Points
//
// Parse: load envelope from JSON (returns empty envelope on blank input).
// Envelope.Encode: serialise envelope to JSON for persistence.
// EpisodeKey: format deterministic "s01e02" keys.
// Assets.AddAsset/FindAsset: record and locate artefacts by stage and episode.
package ripspec
