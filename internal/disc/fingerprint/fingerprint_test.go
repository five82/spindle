package fingerprint

import (
	"context"
	"path/filepath"
	"testing"
)

func TestComputeBluRayFingerprint(t *testing.T) {
	base := filepath.Join("testdata", "bluray")
	got, err := computeBluRayFingerprint(context.Background(), base)
	if err != nil {
		t.Fatalf("computeBluRayFingerprint: %v", err)
	}
	const want = "e2ed4de67e547980753e42fa7bc55182e47de5ce2b4a8d7770c9299801170a11"
	if got != want {
		t.Fatalf("unexpected fingerprint: %s", got)
	}
}

func TestComputeDVDFingerprint(t *testing.T) {
	base := filepath.Join("testdata", "dvd")
	got, err := computeDVDFingerprint(context.Background(), base)
	if err != nil {
		t.Fatalf("computeDVDFingerprint: %v", err)
	}
	const want = "3f92c451767f455ca22f3118e2797f9e6b19f2227d7c6a1a15bef8662aa0653f"
	if got != want {
		t.Fatalf("unexpected fingerprint: %s", got)
	}
}

func TestComputeManifestFingerprint(t *testing.T) {
	base := filepath.Join("testdata", "other")
	got, err := computeManifestFingerprint(context.Background(), base, 4)
	if err != nil {
		t.Fatalf("computeManifestFingerprint: %v", err)
	}
	const want = "c8663aad95f9762b1e9b52ebea865d6b3f78c73d3b0104aa0a6c4d69c689966d"
	if got != want {
		t.Fatalf("unexpected fingerprint: %s", got)
	}
}
