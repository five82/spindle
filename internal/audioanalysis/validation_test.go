package audioanalysis_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"spindle/internal/audioanalysis"
	"spindle/internal/logging"
)

func TestValidateCommentaryLabelingNoCommentaryTracks(t *testing.T) {
	// When expectedCount is 0, validation should pass without probing
	err := audioanalysis.ValidateCommentaryLabeling(context.Background(), "", nil, 0, nil)
	if err != nil {
		t.Fatalf("expected no error for zero expected commentary tracks, got: %v", err)
	}
}

func TestValidateCommentaryLabelingEmptyTargets(t *testing.T) {
	// When targets is empty but expectedCount > 0, should return nil (no files to validate)
	err := audioanalysis.ValidateCommentaryLabeling(context.Background(), "", []string{}, 1, nil)
	if err != nil {
		t.Fatalf("expected no error for empty targets, got: %v", err)
	}
}

func TestValidateCommentaryLabelingWithMockFile(t *testing.T) {
	// Skip if ffprobe is not available
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}

	// Create a temp directory with a simple test file
	// This test would need a real MKV with audio streams to work properly
	// For now, just verify the function handles missing files gracefully
	tmpDir := t.TempDir()
	nonExistentFile := filepath.Join(tmpDir, "nonexistent.mkv")

	logger := logging.NewNop()
	err := audioanalysis.ValidateCommentaryLabeling(context.Background(), "", []string{nonExistentFile}, 1, logger)
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestValidateCommentaryLabelingEmptyPath(t *testing.T) {
	// Empty path in targets should be skipped
	err := audioanalysis.ValidateCommentaryLabeling(context.Background(), "", []string{""}, 1, nil)
	if err != nil {
		t.Fatalf("expected no error for empty path in targets, got: %v", err)
	}
}

// Integration test - requires a real video file with commentary tracks
func TestValidateCommentaryLabelingIntegration(t *testing.T) {
	// Skip by default - this test requires a real video file
	if os.Getenv("TEST_COMMENTARY_VIDEO") == "" {
		t.Skip("skipping integration test - set TEST_COMMENTARY_VIDEO to a path with commentary tracks")
	}

	videoPath := os.Getenv("TEST_COMMENTARY_VIDEO")
	expectedCount := 1 // Adjust based on test file

	logger := logging.NewNop()
	err := audioanalysis.ValidateCommentaryLabeling(context.Background(), "", []string{videoPath}, expectedCount, logger)
	if err != nil {
		t.Errorf("commentary validation failed: %v", err)
	}
}
