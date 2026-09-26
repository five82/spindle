package audio

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/media"
)

func TestEncodeStreamsRejectsInvalidStreams(t *testing.T) {
	if streams, err := EncodeStreams(context.Background(), "missing", t.TempDir(), nil, 1); err != nil || streams != nil {
		t.Fatalf("empty: %v, %v", streams, err)
	}
	missing := filepath.Join(t.TempDir(), "missing.mkv")
	for _, tt := range []struct {
		channels uint32
		message  string
	}{{0, "no channels"}, {9, "unsupported audio channel count"}, {2, ""}} {
		stream := media.AudioStreamInfo{StreamIndex: 3, Channels: tt.channels}
		_, err := EncodeStreams(context.Background(), missing, t.TempDir(), []media.AudioStreamInfo{stream}, 1)
		if err == nil || !strings.Contains(err.Error(), "stream 3:") || (tt.message != "" && !strings.Contains(err.Error(), tt.message)) {
			t.Errorf("channels %d: %v", tt.channels, err)
		}
	}
}
