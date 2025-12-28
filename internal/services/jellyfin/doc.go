// Package jellyfin supplies the organizer with library management primitives.
//
// The default SimpleService moves encoded files into the configured directory
// tree, while the HTTP-backed service can also authenticate with Jellyfin and
// trigger library refreshes when an API key is available. File moving and
// collision handling live here so the organizer can stay focused on workflow
// concerns.
package jellyfin
