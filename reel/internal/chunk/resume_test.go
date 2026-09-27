package chunk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkDirNameUsesCanonicalPathHash(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(input, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}

	otherDir := filepath.Join(dir, "other")
	if err := os.Mkdir(otherDir, 0755); err != nil {
		t.Fatal(err)
	}
	sameBase := filepath.Join(otherDir, "movie.mkv")
	if err := os.WriteFile(sameBase, []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}

	name := WorkDirName(input)
	otherName := WorkDirName(sameBase)
	if name == otherName {
		t.Fatalf("WorkDirName collided for distinct canonical paths: %q", name)
	}
	if !strings.HasPrefix(name, ".reel-movie-") {
		t.Fatalf("WorkDirName() = %q, want .reel-movie-<hash>", name)
	}
}

func TestResumeValidateRequiresExistingMatchingChunkFile(t *testing.T) {
	workDir := t.TempDir()
	if err := EnsureEncodeDir(workDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(IVFPath(workDir, 0), []byte("chunk0"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(IVFPath(workDir, 1), []byte("chunk1"), 0644); err != nil {
		t.Fatal(err)
	}

	resume := &ResumeInf{ChunksDone: []ChunkComp{
		{Idx: 0, Frames: 10, Size: 6},   // valid
		{Idx: 1, Frames: 10, Size: 999}, // size mismatch
		{Idx: 2, Frames: 10, Size: 6},   // missing file
		{Idx: 3, Frames: 99, Size: 6},   // frame mismatch
	}}
	chunks := []Chunk{
		{Idx: 0, Start: 0, End: 10},
		{Idx: 1, Start: 10, End: 20},
		{Idx: 2, Start: 20, End: 30},
		{Idx: 3, Start: 30, End: 40},
	}

	validated := resume.Validate(workDir, chunks)
	if len(validated.ChunksDone) != 1 || validated.ChunksDone[0].Idx != 0 {
		t.Fatalf("validated chunks = %+v, want only chunk 0", validated.ChunksDone)
	}
}

func TestEnsureResumeManifestResetsOnMismatch(t *testing.T) {
	workDir := t.TempDir()
	manifest := ResumeManifest{InputPath: "/media/movie.mkv", Quality: 27}
	if reset, err := EnsureResumeManifest(workDir, manifest); err != nil || reset {
		t.Fatalf("initial manifest write: reset=%v err=%v", reset, err)
	}

	if reset, err := EnsureResumeManifest(workDir, manifest); err != nil || reset {
		t.Fatalf("matching manifest: reset=%v err=%v", reset, err)
	}

	// Stale resume state from the interrupted run: chunk outputs and done.txt
	// must go, the self-validating chunk plan must survive.
	if err := EnsureEncodeDir(workDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(IVFPath(workDir, 0), []byte("chunk0"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "done.txt"), []byte("0 10 6\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "chunk-plan.txt"), []byte("10\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mismatch := manifest
	mismatch.Quality = 28
	reset, err := EnsureResumeManifest(workDir, mismatch)
	if err != nil {
		t.Fatalf("mismatched manifest: %v", err)
	}
	if !reset {
		t.Fatal("mismatched manifest did not reset stale resume state")
	}
	for _, gone := range []string{IVFPath(workDir, 0), filepath.Join(workDir, "done.txt")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("stale resume file survived reset: %s (err=%v)", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(workDir, "chunk-plan.txt")); err != nil {
		t.Fatalf("chunk plan did not survive reset: %v", err)
	}

	// The rewritten manifest matches the new settings.
	if reset, err := EnsureResumeManifest(workDir, mismatch); err != nil || reset {
		t.Fatalf("rewritten manifest: reset=%v err=%v", reset, err)
	}
}

func TestChunkFingerprintChangesWithBoundaries(t *testing.T) {
	chunks := []Chunk{{Idx: 0, Start: 0, End: 10}, {Idx: 1, Start: 10, End: 20}}
	changed := []Chunk{{Idx: 0, Start: 0, End: 12}, {Idx: 1, Start: 12, End: 20}}

	if ChunkFingerprint(chunks) == ChunkFingerprint(changed) {
		t.Fatal("ChunkFingerprint did not change when chunk boundaries changed")
	}
}

// TestEnsureResumeManifestResetsOnTreatmentChange covers the grain-treatment
// fields: chunks encoded denoised with a grain table must never be resumed by
// a run that would encode the rest untreated (or the other way round).
func TestEnsureResumeManifestResetsOnTreatmentChange(t *testing.T) {
	workDir := t.TempDir()
	treated := ResumeManifest{
		InputPath:      "/media/movie.mkv",
		GrainTreatment: "auto",
		Denoise:        "fftdnoiz",
		GrainTable:     "grain-med",
	}
	if reset, err := EnsureResumeManifest(workDir, treated); err != nil || reset {
		t.Fatalf("initial manifest write: reset=%v err=%v", reset, err)
	}

	for name, changed := range map[string]func(m *ResumeManifest){
		"untreated":     func(m *ResumeManifest) { m.Denoise, m.GrainTable = "", "" },
		"other tier":    func(m *ResumeManifest) { m.GrainTable = "grain-light" },
		"gate disabled": func(m *ResumeManifest) { m.GrainTreatment = "off" },
	} {
		t.Run(name, func(t *testing.T) {
			mismatch := treated
			changed(&mismatch)
			reset, err := EnsureResumeManifest(workDir, mismatch)
			if err != nil {
				t.Fatalf("EnsureResumeManifest: %v", err)
			}
			if !reset {
				t.Error("changed grain treatment did not discard stale resume state")
			}
			// Restore the treated manifest for the next case.
			if _, err := EnsureResumeManifest(workDir, treated); err != nil {
				t.Fatalf("restore: %v", err)
			}
		})
	}
}

// TestWriteResumeManifestPinsWithoutComparing covers the grain gate's rewrite:
// the verdict is only known after the directory has been validated, so the
// pipeline overwrites the manifest with it before anything is encoded.
func TestWriteResumeManifestPinsWithoutComparing(t *testing.T) {
	workDir := t.TempDir()
	base := ResumeManifest{InputPath: "/media/movie.mkv", GrainTreatment: "auto"}
	if _, err := EnsureResumeManifest(workDir, base); err != nil {
		t.Fatalf("EnsureResumeManifest: %v", err)
	}

	gated := base
	gated.Denoise = "fftdnoiz"
	gated.GrainTable = "grain-med"
	if err := WriteResumeManifest(workDir, gated); err != nil {
		t.Fatalf("WriteResumeManifest: %v", err)
	}
	if reset, err := EnsureResumeManifest(workDir, gated); err != nil || reset {
		t.Fatalf("pinned manifest should match on the next run: reset=%v err=%v", reset, err)
	}
	if reset, err := EnsureResumeManifest(workDir, base); err != nil || !reset {
		t.Fatalf("a run without the treatment should reset: reset=%v err=%v", reset, err)
	}
}
