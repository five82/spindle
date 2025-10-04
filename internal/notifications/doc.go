// Package notifications delivers workflow events via pluggable notifiers.
//
// The default implementation publishes to ntfy using the topic configured in
// config.toml and gracefully degrades to a no-op when notifications are
// disabled. Enumerated event types cover the major workflow milestones so stage
// handlers can emit consistent, user-friendly messages without duplicating HTTP
// glue.
//
// Extend this package if you need alternative transports; all workflow code
// depends only on the simple Service interface.
package notifications
