package reel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/config"
)

func TestEncodeHandlesMissingInputAndBlockedOutput(t *testing.T) {
	encoder, err := New(WithQualityMode(config.QualityModeCRF))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.mkv")
	if _, err := encoder.Encode(context.Background(), missing, filepath.Join(dir, "output"), nil); err == nil || !strings.Contains(err.Error(), "no files were encoded") {
		t.Fatalf("missing input: %v", err)
	}
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Encode(context.Background(), missing, blocked, nil); err == nil || !strings.Contains(err.Error(), "output directory") {
		t.Fatalf("blocked output: %v", err)
	}
}
