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

func TestComputeBluRayFingerprint_IgnoresCertificate(t *testing.T) {
	// A disc with CERTIFICATE/id.bdmv must still fingerprint using BDMV
	// metadata (playlists, clips, etc.) rather than just the certificate.
	// Multi-disc sets share the same certificate, so using it alone causes
	// fingerprint collisions between different discs in the same set.
	withCert := filepath.Join("testdata", "bluray_with_cert")
	withoutCert := filepath.Join("testdata", "bluray")

	fpWith, err := computeBluRayFingerprint(context.Background(), withCert)
	if err != nil {
		t.Fatalf("with cert: %v", err)
	}
	fpWithout, err := computeBluRayFingerprint(context.Background(), withoutCert)
	if err != nil {
		t.Fatalf("without cert: %v", err)
	}

	// Different BDMV content must produce different fingerprints even if
	// both discs had the same CERTIFICATE/id.bdmv.
	if fpWith == fpWithout {
		t.Fatalf("fingerprints should differ: both produced %s", fpWith)
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
