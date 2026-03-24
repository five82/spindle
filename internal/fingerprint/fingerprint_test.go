package fingerprint

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// hashFiles (content-aware)
// ---------------------------------------------------------------------------

func TestHashFiles_KnownStructure(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "hello")
	writeFile(t, filepath.Join(dir, "b.txt"), "world!")

	files := []string{
		filepath.Join(dir, "a.txt"),
		filepath.Join(dir, "b.txt"),
	}
	hash, err := hashFiles(dir, files, 0)
	if err != nil {
		t.Fatalf("hashFiles: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if len(hash) != 64 {
		t.Fatalf("expected 64-char hex SHA-256, got %d chars", len(hash))
	}
}

func TestHashFiles_SameContentProducesSameHash(t *testing.T) {
	dir1 := t.TempDir()
	writeFile(t, filepath.Join(dir1, "x.bin"), "data")

	dir2 := t.TempDir()
	writeFile(t, filepath.Join(dir2, "x.bin"), "data")

	h1, err := hashFiles(dir1, []string{filepath.Join(dir1, "x.bin")}, 0)
	if err != nil {
		t.Fatalf("hash dir1: %v", err)
	}
	h2, err := hashFiles(dir2, []string{filepath.Join(dir2, "x.bin")}, 0)
	if err != nil {
		t.Fatalf("hash dir2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("same content produced different hashes: %s vs %s", h1, h2)
	}
}

func TestHashFiles_DifferentContentProducesDifferentHash(t *testing.T) {
	dir1 := t.TempDir()
	writeFile(t, filepath.Join(dir1, "a.txt"), "aaa")

	dir2 := t.TempDir()
	writeFile(t, filepath.Join(dir2, "a.txt"), "bbb")

	h1, err := hashFiles(dir1, []string{filepath.Join(dir1, "a.txt")}, 0)
	if err != nil {
		t.Fatalf("hash dir1: %v", err)
	}
	h2, err := hashFiles(dir2, []string{filepath.Join(dir2, "a.txt")}, 0)
	if err != nil {
		t.Fatalf("hash dir2: %v", err)
	}
	if h1 == h2 {
		t.Error("different content produced the same hash")
	}
}

func TestHashFiles_SameSizeDifferentContentProducesDifferentHash(t *testing.T) {
	// Old path+size hashing would not distinguish these.
	dir1 := t.TempDir()
	writeFile(t, filepath.Join(dir1, "a.txt"), "abc")

	dir2 := t.TempDir()
	writeFile(t, filepath.Join(dir2, "a.txt"), "xyz")

	h1, err := hashFiles(dir1, []string{filepath.Join(dir1, "a.txt")}, 0)
	if err != nil {
		t.Fatalf("hash dir1: %v", err)
	}
	h2, err := hashFiles(dir2, []string{filepath.Join(dir2, "a.txt")}, 0)
	if err != nil {
		t.Fatalf("hash dir2: %v", err)
	}
	if h1 == h2 {
		t.Error("same size, different content should produce different hashes")
	}
}

func TestHashFiles_MaxBytesCapReading(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "big.bin"), "0123456789abcdef")

	full, err := hashFiles(dir, []string{filepath.Join(dir, "big.bin")}, 0)
	if err != nil {
		t.Fatalf("full hash: %v", err)
	}
	capped, err := hashFiles(dir, []string{filepath.Join(dir, "big.bin")}, 4)
	if err != nil {
		t.Fatalf("capped hash: %v", err)
	}
	if full == capped {
		t.Error("full and capped hashes should differ when content exceeds maxBytes")
	}
}

func TestHashFiles_EmptyFileList(t *testing.T) {
	dir := t.TempDir()
	h1, err := hashFiles(dir, nil, 0)
	if err != nil {
		t.Fatalf("hashFiles: %v", err)
	}
	h2, err := hashFiles(dir, nil, 0)
	if err != nil {
		t.Fatalf("hashFiles second call: %v", err)
	}
	if h1 != h2 {
		t.Errorf("empty file list hash not consistent: %s vs %s", h1, h2)
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
	mkdirAll(t, filepath.Join(bdmv, "PLAYLIST"))
	writeFile(t, filepath.Join(bdmv, "PLAYLIST", "00001.mpls"), "playlist")
	mkdirAll(t, filepath.Join(bdmv, "CLIPINF"))
	writeFile(t, filepath.Join(bdmv, "CLIPINF", "00001.clpi"), "clipinfo")
	// STREAM should be excluded.
	mkdirAll(t, filepath.Join(bdmv, "STREAM"))
	writeFile(t, filepath.Join(bdmv, "STREAM", "00001.m2ts"), "stream-data")

	fp, err := blurayFingerprint(dir)
	if err != nil {
		t.Fatalf("blurayFingerprint: %v", err)
	}
	if fp == "" {
		t.Fatal("expected non-empty fingerprint for Blu-ray structure")
	}

	// Changing stream data should NOT change the fingerprint.
	writeFile(t, filepath.Join(bdmv, "STREAM", "00001.m2ts"), "different-stream")
	fp2, err := blurayFingerprint(dir)
	if err != nil {
		t.Fatalf("blurayFingerprint after stream change: %v", err)
	}
	if fp != fp2 {
		t.Error("Blu-ray fingerprint should not change when STREAM data changes")
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

func TestBlurayFingerprint_ContentSensitive(t *testing.T) {
	dir1 := t.TempDir()
	bdmv1 := filepath.Join(dir1, "BDMV")
	mkdirAll(t, bdmv1)
	writeFile(t, filepath.Join(bdmv1, "index.bdmv"), "idx-v1")

	dir2 := t.TempDir()
	bdmv2 := filepath.Join(dir2, "BDMV")
	mkdirAll(t, bdmv2)
	writeFile(t, filepath.Join(bdmv2, "index.bdmv"), "idx-v2")

	fp1, _ := blurayFingerprint(dir1)
	fp2, _ := blurayFingerprint(dir2)
	if fp1 == fp2 {
		t.Error("different index.bdmv content should produce different fingerprints")
	}
}

// ---------------------------------------------------------------------------
// dvdFingerprint
// ---------------------------------------------------------------------------

func TestDVDFingerprint_MockStructure(t *testing.T) {
	dir := t.TempDir()
	vts := filepath.Join(dir, "VIDEO_TS")
	mkdirAll(t, vts)
	writeFile(t, filepath.Join(vts, "VIDEO_TS.IFO"), "ifo-data")
	writeFile(t, filepath.Join(vts, "VTS_01_0.IFO"), "vts-ifo")
	// VOB files should be excluded.
	writeFile(t, filepath.Join(vts, "VTS_01_0.VOB"), "vob-data")

	fp, err := dvdFingerprint(dir)
	if err != nil {
		t.Fatalf("dvdFingerprint: %v", err)
	}
	if fp == "" {
		t.Fatal("expected non-empty fingerprint for DVD structure")
	}

	// Changing VOB data should NOT change the fingerprint.
	writeFile(t, filepath.Join(vts, "VTS_01_0.VOB"), "different-vob")
	fp2, err := dvdFingerprint(dir)
	if err != nil {
		t.Fatalf("dvdFingerprint after VOB change: %v", err)
	}
	if fp != fp2 {
		t.Error("DVD fingerprint should not change when VOB data changes")
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

	bdmv := filepath.Join(dir, "BDMV")
	mkdirAll(t, bdmv)
	writeFile(t, filepath.Join(bdmv, "index.bdmv"), "idx")

	vts := filepath.Join(dir, "VIDEO_TS")
	mkdirAll(t, vts)
	writeFile(t, filepath.Join(vts, "VIDEO_TS.IFO"), "ifo")

	genFP, err := Generate(dir, nil)
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

	genFP, err := Generate(dir, nil)
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

func TestGenerate_FallsBackToFullContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "random.dat"), "stuff")

	genFP, err := Generate(dir, nil)
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
// collectGlob
// ---------------------------------------------------------------------------

func TestCollectGlob(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.mpls"), "p1")
	writeFile(t, filepath.Join(dir, "b.mpls"), "p2")
	writeFile(t, filepath.Join(dir, "c.txt"), "t1")

	matches := collectGlob(dir, "*.mpls")
	if len(matches) != 2 {
		t.Fatalf("expected 2 .mpls matches, got %d", len(matches))
	}
}

// ---------------------------------------------------------------------------
// readFileContent
// ---------------------------------------------------------------------------

func TestReadFileContent_Full(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	writeFile(t, path, "0123456789")

	content, err := readFileContent(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "0123456789" {
		t.Errorf("got %q, want full content", content)
	}
}

func TestReadFileContent_Capped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	writeFile(t, path, "0123456789")

	content, err := readFileContent(path, 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "01234" {
		t.Errorf("got %q, want first 5 bytes", content)
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
