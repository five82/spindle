package processing

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/five82/reel/internal/encode"
)

func TestEncodePipelineErrorReportsVideoWhenAudioWasCanceledByVideoFailure(t *testing.T) {
	videoErr := errors.New("svt failed")
	audioErr := fmt.Errorf("stream 1: %w", context.Canceled)

	got := encodePipelineError(nil, videoErr, audioErr)
	if got.Error() != "chunked encoding failed: svt failed" {
		t.Fatalf("encodePipelineError() = %q", got)
	}
}

func TestEncodePipelineErrorReportsAudioWhenAudioCanceledVideo(t *testing.T) {
	audioErr := errors.New("decode failed")

	got := encodePipelineError(nil, context.Canceled, audioErr)
	if got.Error() != "audio encoding failed: decode failed" {
		t.Fatalf("encodePipelineError() = %q", got)
	}
}

func TestEncodePipelineErrorPreservesParentCancellation(t *testing.T) {
	got := encodePipelineError(context.Canceled, errors.New("svt failed"), errors.New("decode failed"))
	if !errors.Is(got, context.Canceled) {
		t.Fatalf("encodePipelineError() = %v, want context canceled", got)
	}
}

func TestEncodePipelineErrorReportsMemoryPressure(t *testing.T) {
	got := encodePipelineError(nil, encode.ErrMemoryPressure, errors.New("decode failed"))
	if !errors.Is(got, encode.ErrMemoryPressure) {
		t.Fatalf("encodePipelineError() = %v, want memory pressure", got)
	}
}
