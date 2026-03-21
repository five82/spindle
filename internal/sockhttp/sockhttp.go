// Package sockhttp provides helpers for HTTP communication over Unix sockets.
package sockhttp

import (
	"context"
	"net"
	"net/http"
	"time"
)

// NewUnixClient creates an http.Client that dials the given Unix socket.
func NewUnixClient(socketPath string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

// SetAuth sets the Authorization header if token is non-empty.
func SetAuth(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
