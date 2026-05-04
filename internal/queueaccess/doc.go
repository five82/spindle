// Package queueaccess provides daemon-backed queue access for the CLI.
// HTTPAccess connects to the daemon's HTTP API over a Unix socket; normal queue
// reads and mutations do not fall back to direct SQLite access.
package queueaccess
