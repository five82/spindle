// Package encodingstate captures Drapto encoding telemetry in a transport-
// friendly form. The Snapshot type aggregates progress, configuration,
// validation, and result data emitted by Drapto's --progress-json stream.
//
// # Purpose
//
// During encoding, Drapto emits JSON events for stage transitions, frame
// progress, crop detection, validation checks, warnings, errors, and
// completion. The encoding stage parses these events and accumulates them
// into a Snapshot, which is persisted to queue.encoding_details_json and
// exposed via the API for live progress display.
//
// # Key Types
//
// Snapshot: root container with job label, episode key, stage, percent,
// ETA, speed, FPS, bitrate, frame counts, and optional nested structs.
//
// Hardware: encoder hostname.
// Video: input/output files, duration, resolution, dynamic range.
// Crop: crop detection results (crop string, required/disabled flags).
// Config: encoder settings (preset, tune, quality, audio codec, SVT params).
// Validation: pass/fail status and per-step details.
// Issue: warning/error with title, message, context, suggestion.
// Result: completion summary with sizes, streams, speed, and reduction percent.
//
// # Entry Points
//
// Unmarshal: parse Snapshot from JSON string (empty input yields empty snapshot).
// Snapshot.Marshal: serialise to JSON for persistence.
// Snapshot.IsZero: check if snapshot has no meaningful data.
package encodingstate
