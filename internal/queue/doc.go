// Package queue persists workflow items in SQLite and exposes helpers for
// driving their lifecycle.
//
// The Store manages database connections, schema initialization, stats queries,
// heartbeat tracking, stuck-item recovery, and status transitions that mirror
// the public workflow enum. Queue items capture progress, media metadata,
// fingerprints, and review flags so stages can coordinate without additional
// state.
//
// The database is treated as transient storage for in-flight jobs rather than
// a long-term archive. Schema changes bump the version in schema.go; users
// clear the database to adopt the new schema.
//
// Treat this package as the single source of truth for queue semantics; when you
// add new statuses or metadata fields, update schema.sql and bump schemaVersion.
package queue
