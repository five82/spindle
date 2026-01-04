// Package ffprobe provides a typed wrapper around ffprobe JSON output.
//
// This package has no spindle-specific dependencies and could be extracted
// as a standalone library.
//
// Key types:
//   - Result: parsed ffprobe output containing streams and format metadata
//   - Stream: individual audio/video/subtitle stream properties
//   - Format: container-level metadata (duration, size, bitrate)
//
// Primary entry point:
//   - Inspect: executes ffprobe and returns parsed Result
//
// Helper methods on Result provide convenient access to stream counts,
// duration parsing, and bitrate extraction.
package ffprobe
