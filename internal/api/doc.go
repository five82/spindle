// Package api defines wire-format types and converters for the IPC and HTTP
// API layer. It translates internal queue models into transport-friendly DTOs
// that Flyer and other consumers can render without coupling to internal types.
//
// # Key Types
//
// QueueItem: transport representation of a queue entry with progress, encoding
// status, episode details, and subtitle generation summary.
//
// WorkflowStatus: daemon running state, queue stats, stage health, and last item.
//
// DaemonStatus: aggregated runtime information including dependencies.
//
// LogEvent/LogStreamResponse: structured log payloads for live tailing.
//
// # Converters
//
// FromQueueItem: queue.Item -> QueueItem with progress stage defaults, episode
// status derivation from RipSpec, and encoding details unmarshalling.
//
// FromStatusSummary: workflow.StatusSummary -> WorkflowStatus.
//
// StageHealthSlice: deterministic ordering of stage health map.
//
// # Design Notes
//
// DTOs use camelCase JSON tags for JavaScript/TypeScript consumers. Internal
// enums (queue.Status, queue.Lane) are exposed as lowercase strings. Timestamps
// use RFC3339 with milliseconds. RipSpec and Metadata are passed through as
// json.RawMessage to avoid double-encoding.
//
// Episode statuses are derived from RipSpec assets rather than stored separately,
// so the API always reflects the current state of realised artefacts.
package api
