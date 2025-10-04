// Package disc interfaces with physical optical drives and MakeMKV scanning
// utilities.
//
// It provides scanners that translate MakeMKV output into structured metadata,
// fingerprinting utilities for duplicate detection, and ejector helpers so the
// ripping stage can safely release discs. Parsers live here to keep low-level
// device quirks isolated from higher-level workflow code.
package disc
