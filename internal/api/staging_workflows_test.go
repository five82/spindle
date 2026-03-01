package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type fingerprintStub struct {
	fps map[string]struct{}
	err error
}

func (s fingerprintStub) ActiveFingerprints(_ context.Context) (map[string]struct{}, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.fps, nil
}

func TestCleanStagingDirectoriesNotConfigured(t *testing.T) {
	result, err := CleanStagingDirectories(context.Background(), CleanStagingRequest{})
	if err != nil {
		t.Fatalf("CleanStagingDirectories: %v", err)
	}
	if result.Configured {
		t.Fatal("Configured = true, want false")
	}
}

func TestCleanStagingDirectoriesCleanAll(t *testing.T) {
	dir := t.TempDir()
	oldDir := filepath.Join(dir, "old")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir old dir: %v", err)
	}

	result, err := CleanStagingDirectories(context.Background(), CleanStagingRequest{
		StagingDir: dir,
		CleanAll:   true,
	})
	if err != nil {
		t.Fatalf("CleanStagingDirectories: %v", err)
	}
	if !result.Configured {
		t.Fatal("Configured = false, want true")
	}
	if result.Scope != "staging" {
		t.Fatalf("Scope = %q, want staging", result.Scope)
	}
	if len(result.Cleanup.Removed) != 1 {
		t.Fatalf("removed = %d, want 1", len(result.Cleanup.Removed))
	}
}

func TestCleanStagingDirectoriesOrphaned(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "ACTIVEFP")
	orphan := filepath.Join(dir, "ORPHANFP")
	for _, d := range []string{active, orphan} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	result, err := CleanStagingDirectories(context.Background(), CleanStagingRequest{
		StagingDir: dir,
		Fingerprints: fingerprintStub{fps: map[string]struct{}{
			"ACTIVEFP": {},
		}},
	})
	if err != nil {
		t.Fatalf("CleanStagingDirectories: %v", err)
	}
	if result.Scope != "orphaned staging" {
		t.Fatalf("Scope = %q, want orphaned staging", result.Scope)
	}
	if len(result.Cleanup.Removed) != 1 {
		t.Fatalf("removed = %d, want 1", len(result.Cleanup.Removed))
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active dir should remain: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan dir should be removed, stat err=%v", err)
	}
}
