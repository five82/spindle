// Package workflow advances queue items through the configured processing
// stages.
//
// The Manager polls the queue, reclaims stale work via heartbeats, and feeds
// items into registered stage handlers (identifier, ripper, audio analysis,
// episode identifier, encoder, subtitles, organizer) while capturing progress
// and failure metadata. It also aggregates queue stats, calls stage health
// checks, and emits queue-level notifications when processing starts or
// completes.
//
// The workflow runs two independent lanes: foreground (disc identification, ripping)
// and background (audio analysis, episode identification, encoding, subtitles,
// organizing). Each lane
// polls for items matching its statuses and processes them independently, enabling
// parallel execution where ripping of disc B can proceed while disc A encodes.
//
// Add new lifecycle stages by extending StageSet, updating the queue status
// enums, and teaching the manager how to transition items; this package is the
// authoritative home for that coordination logic.
package workflow
