// Package config loads, normalizes, and validates Spindle configuration data.
//
// It supplies repository defaults, expands user paths (including tilde
// shortcuts), reads TOML files, and honours environment fallbacks such as
// TMDB_API_KEY. The Config type centralizes every knob the daemon and CLI need,
// allowing staging/library directories and external service credentials to be
// discovered in one pass.
//
// Always obtain settings through this package so downstream code receives
// sanitized paths, canonical log formats, and clear validation errors.
package config
