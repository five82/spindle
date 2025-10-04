// Package encoding runs the Drapto-based encoding stage for queue items.
//
// It stages ripped files into an encoded directory, drives the drapto client
// while persisting progress callbacks, and records final artifact paths for the
// organizer. The handler emits ntfy notifications on success, surfaces
// structured errors when external tools fail, and can fall back to placeholder
// copies during development to keep the pipeline moving.
//
// Keep additional encoding logic here so the workflow manager and organizer can
// assume a single source of truth for encoded artifacts.
package encoding
