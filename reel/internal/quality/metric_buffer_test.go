//go:build cgo && !no_vship

package quality

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/five82/reel/internal/video"
)

func TestMetricBufferInvalidSize(t *testing.T) {
	for _, size := range []int{0, -1} {
		buf, release, err := newMetricBuffer(size)
		if err == nil || buf != nil || release != nil {
			t.Fatalf("size %d: expected allocation failure", size)
		}
	}
}

func TestMetricBufferGPU(t *testing.T) {
	if os.Getenv("REEL_TEST_GPU") == "" {
		t.Skip("set REEL_TEST_GPU=1 for native pinned-buffer checks")
	}
	for _, size := range []int{1, 17, 1920 * 1080 * 3} {
		first, freeFirst, err := newMetricBuffer(size)
		if err != nil {
			t.Fatal(err)
		}
		defer freeFirst()
		second, freeSecond, err := newMetricBuffer(size)
		if err != nil {
			t.Fatal(err)
		}
		defer freeSecond()
		for i := range first {
			first[i] = byte(i)
			second[i] = byte(i ^ 0xff)
		}
		for i := range first {
			if first[i] != byte(i) || second[i] != byte(i^0xff) {
				t.Fatalf("overlapping/corrupt buffers at %d", i)
			}
		}
	}
}

func TestMetricBufferCancellationGPU(t *testing.T) {
	if os.Getenv("REEL_TEST_GPU") == "" {
		t.Skip("set REEL_TEST_GPU=1 for native pinned-buffer checks")
	}
	info := &video.Info{Width: 64, Height: 64, FPSNum: 24, FPSDen: 1}
	display, err := EnsureDisplayModel(t.TempDir(), info, "")
	if err != nil {
		t.Fatal(err)
	}
	proc, err := NewVshipProcessor(64, 64, info, display)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := proc.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, resume := make(chan struct{}), make(chan struct{})
	read := func(_ int, buf []byte) error {
		close(entered)
		<-resume
		// The C allocation must remain alive while a canceled producer exits.
		for i := range buf {
			buf[i] = byte(i)
		}
		return ctx.Err()
	}
	finished := make(chan error, 1)
	go func() {
		_, err := computeCVVDPFrames(ctx, proc, 64, 64, 8, read, read, nil)
		finished <- err
	}()
	select {
	case <-entered:
	case err := <-finished:
		t.Fatalf("compute failed before reader: %v", err)
	}
	cancel()
	select {
	case err := <-finished:
		close(resume)
		t.Fatalf("compute returned with producer still holding C memory: %v", err)
	default:
	}
	close(resume)
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
