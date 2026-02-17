// Package fingerprint computes deterministic fingerprints for optical discs.
//
// This package has no spindle-specific dependencies and could be extracted
// as a standalone library.
//
// Fingerprinting strategies by disc type:
//   - Blu-ray: uses CERTIFICATE/id.bdmv or BDMV/index.bdmv structure
//   - DVD: uses VIDEO_TS/VIDEO_TS.IFO structure
//   - Fallback: hashes directory manifest (first 64 KiB of each file)
//
// The fingerprint is a SHA-256 hash that uniquely identifies disc content,
// enabling duplicate detection and rip cache lookups.
//
// Primary entry points:
//   - Compute: generates fingerprint from mounted disc device
//   - TitleHash: generates a content-based hash from title metadata for cache matching
package fingerprint
