// Package plex supplies the organizer with library management primitives.
//
// The default SimpleService moves encoded files into the configured directory
// tree, while the HTTP-backed service can also authenticate with Plex, resolve
// library section keys, and trigger section refreshes when a valid token is
// available. Token storage and refresh, as well as filesystem collision
// handling, live here so the organizer can stay focused on workflow concerns.
//
// Reuse these helpers when adding new Plex-related behaviours instead of
// reinventing filesystem moves or HTTP glue in stage packages.
package plex
