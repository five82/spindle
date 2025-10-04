// Package ipc exposes the daemon over JSON-RPC Unix sockets and ships the
// matching client used by the CLI.
//
// It owns socket lifecycle management, request/response DTOs, and conversions
// between queue models and lightweight wire representations. The server embeds
// the daemon while the client decorates calls with context timeouts so CLI
// commands fail fast when the daemon is offline.
//
// Reuse these types when adding new RPC endpoints to keep the protocol stable
// and compatible with existing command implementations.
package ipc
