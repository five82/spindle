package stagingdir

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStagingDirectoryFailureAndCancellation(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := ListDirectories(missing); err == nil {
		t.Fatal("missing staging root must fail")
	}
	if result := CleanStale(context.Background(), missing, time.Hour, nil, testLogger()); len(result.Errors) != 1 || result.Removed != 0 {
		t.Fatalf("missing root: %+v", result)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "old")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := CleanStale(ctx, root, time.Hour, nil, testLogger()); len(result.Errors) != 1 || result.Errors[0] != context.Canceled {
		t.Fatalf("canceled cleanup: %+v", result)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("canceled cleanup removed directory: %v", err)
	}
	if _, err := dirSize(missing); err == nil {
		t.Fatal("walking missing directory must fail")
	}
}
