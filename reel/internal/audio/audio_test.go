package audio

import "testing"

func TestReorderSurroundPreservesChannelCount(t *testing.T) {
	buf := []float32{0, 1, 2, 3, 4, 5}
	reorderSurround(buf, 6)

	want := []float32{0, 2, 1, 4, 5, 3}
	if len(buf) != len(want) {
		t.Fatalf("len(buf) = %d, want %d", len(buf), len(want))
	}
	for i := range want {
		if buf[i] != want[i] {
			t.Fatalf("buf[%d] = %v, want %v (full buffer: %v)", i, buf[i], want[i], buf)
		}
	}
}

func TestAudioSampleLimitAccountsForStartOffset(t *testing.T) {
	const videoDuration = 60.0
	if got, want := audioSampleLimit(videoDuration, 0), int64(60*outputSampleRate); got != want {
		t.Fatalf("audioSampleLimit() = %d, want %d", got, want)
	}
	if got, want := audioSampleLimit(videoDuration, 0.5), int64(59.5*outputSampleRate); got != want {
		t.Fatalf("audioSampleLimit() with start offset = %d, want %d", got, want)
	}
}

func TestAudioPath(t *testing.T) {
	if got, want := AudioPath("/tmp/reel", 3), "/tmp/reel/audio_03.opus"; got != want {
		t.Fatalf("AudioPath() = %q, want %q", got, want)
	}
}
