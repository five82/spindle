// Package organizer finalizes processed items by moving encoded media into the
// library and triggering follow-up actions.
//
// It resolves metadata to derive filesystem targets, handles collision-safe
// moves, refreshes Jellyfin when credentials are configured, and routes ambiguous
// items into the review directory with appropriate notifications. Progress
// updates and error wrapping follow the same conventions as other stages so the
// workflow manager can react uniformly.
//
// Extend organization behaviour here whenever post-encoding automation changes.
package organizer
