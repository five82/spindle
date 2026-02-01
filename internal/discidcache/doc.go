// Package discidcache provides a local cache that maps Blu-ray disc IDs to TMDB IDs.
//
// This cache eliminates the need for fuzzy name-based TMDB search for previously
// identified discs. When a disc ID is found in the cache, Spindle can skip KeyDB
// lookup, title parsing, TMDB search, and confidence scoring, fetching fresh
// metadata directly from TMDB using the cached ID.
//
// # Storage
//
// The cache is stored as a JSON file at a configurable path (default:
// ~/.cache/spindle/discid_cache.json). The JSON format is human-readable
// and easy to inspect or edit manually.
//
// # Usage
//
// The cache is disabled by default. Enable it in config.toml:
//
//	[disc_id_cache]
//	enabled = true
//	path = "~/.cache/spindle/discid_cache.json"
//
// CLI commands for inspection and management:
//
//	spindle discid list              # List all cached mappings
//	spindle discid remove <number>   # Remove entry by number from list
//	spindle discid clear             # Remove all entries
package discidcache
