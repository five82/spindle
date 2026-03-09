// Package queueaccess provides a unified Access interface for reading queue
// state. StoreAccess wraps a direct queue.Store for in-process use, while
// HTTPAccess connects to the daemon's HTTP API over a Unix socket.
// OpenWithFallback tries HTTP first and falls back to direct store access.
package queueaccess
