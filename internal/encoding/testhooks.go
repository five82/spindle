package encoding

import (
	"context"

	"spindle/internal/media/ffprobe"
)

// SetProbeForTests overrides the ffprobe runner during tests.
func SetProbeForTests(fn func(context.Context, string, string) (ffprobe.Result, error)) func() {
	previous := encodeProbe
	encodeProbe = fn
	return func() {
		encodeProbe = previous
	}
}
