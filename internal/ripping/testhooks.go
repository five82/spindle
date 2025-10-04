package ripping

import (
	"context"

	"spindle/internal/media/ffprobe"
)

// SetProbeForTests overrides the ffprobe runner during tests.
func SetProbeForTests(fn func(context.Context, string, string) (ffprobe.Result, error)) func() {
	previous := probeVideo
	probeVideo = fn
	return func() {
		probeVideo = previous
	}
}
