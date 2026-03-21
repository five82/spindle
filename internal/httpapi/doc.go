// Package httpapi provides an HTTP API server for Spindle queue operations.
// It supports both Unix socket and TCP listeners, with optional bearer token
// authentication. The /api/health endpoint is unauthenticated; all other
// endpoints require a valid token when one is configured.
package httpapi
