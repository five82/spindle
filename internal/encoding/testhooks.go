package encoding

import (
	"context"

	"spindle/internal/media/ffprobe"
)

// encodeProbe is the ffprobe function used by the encoding package.
// It is a package-level variable so tests can override it.
var encodeProbe = ffprobe.Inspect

// SetProbeForTests overrides the ffprobe runner during tests.
func SetProbeForTests(fn func(context.Context, string, string) (ffprobe.Result, error)) func() {
	previous := encodeProbe
	encodeProbe = fn
	return func() {
		encodeProbe = previous
	}
}
