// Package queue persists workflow items in SQLite and exposes helpers for
// driving their lifecycle.
//
// The Store manages database connections, schema migrations, stats queries,
// heartbeat tracking, stuck-item recovery, and status transitions that mirror
// the public workflow enum. Queue items capture progress, media metadata,
// fingerprints, and review flags so stages can coordinate without additional
// state.
//
// Treat this package as the single source of truth for queue semantics; when you
// add new statuses, metadata fields, or maintenance routines, update the
// migrations and transition helpers here first.
package queue
