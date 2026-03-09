package fingerprint

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// hashFileManifest
// ---------------------------------------------------------------------------

func TestHashFileManifest_KnownStructure(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "hello")
	writeFile(t, filepath.Join(dir, "b.txt"), "world!")

	hash, err := hashFileManifest(dir)
	if err != nil {
		t.Fatalf("hashFileManifest: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if len(hash) != 64 {
		t.Fatalf("expected 64-char hex SHA-256, got %d chars", len(hash))
	}
}

func TestHashFileManifest_SameFilesProduceSameHash(t *testing.T) {
	dir1 := t.TempDir()
	writeFile(t, filepath.Join(dir1, "x.bin"), "data")

	dir2 := t.TempDir()
	writeFile(t, filepath.Join(dir2, "x.bin"), "data")

	h1, err := hashFileManifest(dir1)
	if err != nil {
		t.Fatalf("hash dir1: %v", err)
	}
	h2, err := hashFileManifest(dir2)
	if err != nil {
		t.Fatalf("hash dir2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("same files produced different hashes: %s vs %s", h1, h2)
	}
}

func TestHashFileManifest_DifferentFilesProduceDifferentHash(t *testing.T) {
	dir1 := t.TempDir()
	writeFile(t, filepath.Join(dir1, "a.txt"), "aaa")

	dir2 := t.TempDir()
	writeFile(t, filepath.Join(dir2, "a.txt"), "aaaa") // different size

	h1, err := hashFileManifest(dir1)
	if err != nil {
		t.Fatalf("hash dir1: %v", err)
	}
	h2, err := hashFileManifest(dir2)
	if err != nil {
		t.Fatalf("hash dir2: %v", err)
	}
	if h1 == h2 {
		t.Error("different files produced the same hash")
	}
}

func TestHashFileManifest_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	h1, err := hashFileManifest(dir)
	if err != nil {
		t.Fatalf("hashFileManifest: %v", err)
	}
	// Run again to confirm consistency.
	h2, err := hashFileManifest(dir)
	if err != nil {
		t.Fatalf("hashFileManifest second call: %v", err)
	}
	if h1 != h2 {
		t.Errorf("empty dir hash not consistent: %s vs %s", h1, h2)
	}
}

// ---------------------------------------------------------------------------
// blurayFingerprint
// ---------------------------------------------------------------------------

func TestBlurayFingerprint_MockStructure(t *testing.T) {
	dir := t.TempDir()
	bdmv := filepath.Join(dir, "BDMV")
	mkdirAll(t, bdmv)
	writeFile(t, filepath.Join(bdmv, "index.bdmv"), "idx")
	writeFile(t, filepath.Join(bdmv, "MovieObject.bdmv"), "obj")
	mkdirAll(t, filepath.Join(bdmv, "STREAM"))
	writeFile(t, filepath.Join(bdmv, "STREAM", "00001.m2ts"), "stream")

	fp, err := blurayFingerprint(dir)
	if err != nil {
		t.Fatalf("blurayFingerprint: %v", err)
	}
	if fp == "" {
		t.Fatal("expected non-empty fingerprint for Blu-ray structure")
	}
}

func TestBlurayFingerprint_MissingIndex(t *testing.T) {
	dir := t.TempDir()
	mkdirAll(t, filepath.Join(dir, "BDMV"))

	fp, err := blurayFingerprint(dir)
	if err == nil && fp != "" {
		t.Fatal("expected empty result when index.bdmv is missing")
	}
}

// ---------------------------------------------------------------------------
// dvdFingerprint
// ---------------------------------------------------------------------------

func TestDVDFingerprint_MockStructure(t *testing.T) {
	dir := t.TempDir()
	vts := filepath.Join(dir, "VIDEO_TS")
	mkdirAll(t, vts)
	writeFile(t, filepath.Join(vts, "VIDEO_TS.IFO"), "ifo")
	writeFile(t, filepath.Join(vts, "VTS_01_0.VOB"), "vob")

	fp, err := dvdFingerprint(dir)
	if err != nil {
		t.Fatalf("dvdFingerprint: %v", err)
	}
	if fp == "" {
		t.Fatal("expected non-empty fingerprint for DVD structure")
	}
}

func TestDVDFingerprint_MissingVideoTS(t *testing.T) {
	dir := t.TempDir()

	fp, err := dvdFingerprint(dir)
	if err == nil && fp != "" {
		t.Fatal("expected empty result when VIDEO_TS is missing")
	}
}

// ---------------------------------------------------------------------------
// Generate (strategy ordering)
// ---------------------------------------------------------------------------

func TestGenerate_PrefersBluray(t *testing.T) {
	dir := t.TempDir()

	// Create both Blu-ray and DVD structures.
	bdmv := filepath.Join(dir, "BDMV")
	mkdirAll(t, bdmv)
	writeFile(t, filepath.Join(bdmv, "index.bdmv"), "idx")

	vts := filepath.Join(dir, "VIDEO_TS")
	mkdirAll(t, vts)
	writeFile(t, filepath.Join(vts, "VIDEO_TS.IFO"), "ifo")

	genFP, err := Generate(dir)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	brFP, err := blurayFingerprint(dir)
	if err != nil {
		t.Fatalf("blurayFingerprint: %v", err)
	}
	if genFP != brFP {
		t.Errorf("Generate chose non-Blu-ray strategy: got %s, want %s", genFP, brFP)
	}
}

func TestGenerate_FallsThroughToDVD(t *testing.T) {
	dir := t.TempDir()

	vts := filepath.Join(dir, "VIDEO_TS")
	mkdirAll(t, vts)
	writeFile(t, filepath.Join(vts, "VIDEO_TS.IFO"), "ifo")

	genFP, err := Generate(dir)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dvdFP, err := dvdFingerprint(dir)
	if err != nil {
		t.Fatalf("dvdFingerprint: %v", err)
	}
	if genFP != dvdFP {
		t.Errorf("Generate chose non-DVD strategy: got %s, want %s", genFP, dvdFP)
	}
}

func TestGenerate_FallsBackToFullManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "random.dat"), "stuff")

	genFP, err := Generate(dir)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	fbFP, err := fallbackFingerprint(dir)
	if err != nil {
		t.Fatalf("fallbackFingerprint: %v", err)
	}
	if genFP != fbFP {
		t.Errorf("Generate chose non-fallback strategy: got %s, want %s", genFP, fbFP)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
