package chunk

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadAndValidateSegments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "boundaries.txt")
	if err := os.WriteFile(path, []byte("90\n\n0\n30\n30\n"), 0600); err != nil {
		t.Fatal(err)
	}
	segments, err := LoadSegments(path, 120)
	want := []Segment{{0, 30}, {30, 90}, {90, 120}}
	if err != nil || !reflect.DeepEqual(segments, want) {
		t.Fatalf("segments = %+v, %v", segments, err)
	}
	chunks := Chunkify(segments)
	if len(chunks) != 3 || chunks[1] != (Chunk{Idx: 1, Start: 30, End: 90}) {
		t.Fatalf("chunks = %+v", chunks)
	}
	if err := ValidateSegments(segments, 24, 1); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		segments []Segment
		num, den uint32
		want     string
	}{
		{nil, 24, 1, "no segments"},
		{segments, 24, 0, "denominator"},
		{[]Segment{{0, 1001}}, 30, 1, "too long"},
		{[]Segment{{30, 30}}, 24, 1, "invalid length"},
	} {
		if err := ValidateSegments(tt.segments, tt.num, tt.den); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("ValidateSegments(%v) = %v, want %q", tt.segments, err, tt.want)
		}
	}
	if _, err := LoadSegments(filepath.Join(dir, "absent"), 100); err == nil {
		t.Fatal("missing boundary file accepted")
	}
	if err := os.WriteFile(path, []byte("0\nbad\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSegments(path, 100); err == nil || !strings.Contains(err.Error(), "invalid frame") {
		t.Fatalf("invalid boundary = %v", err)
	}
	if err := os.WriteFile(path, []byte("40\n"), 0600); err != nil {
		t.Fatal(err)
	}
	segments, err = LoadSegments(path, 100)
	if err != nil || !reflect.DeepEqual(segments, []Segment{{0, 40}, {40, 100}}) {
		t.Fatalf("implicit zero = %v, %v", segments, err)
	}
}

func TestResumeDoneFileAndValidation(t *testing.T) {
	dir := t.TempDir()
	resume, err := GetResume(dir)
	if err != nil || len(resume.ChunksDone) != 0 {
		t.Fatalf("missing resume = %+v, %v", resume, err)
	}
	if err := EnsureEncodeDir(dir); err != nil {
		t.Fatal(err)
	}
	first := ChunkComp{Idx: 0, Frames: 10, Size: 3}
	second := ChunkComp{Idx: 1, Frames: 20, Size: 4}
	for _, c := range []ChunkComp{second, first} {
		if err := AppendDone(c, dir); err != nil {
			t.Fatal(err)
		}
	}
	donePath := filepath.Join(dir, "done.txt")
	f, err := os.OpenFile(donePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("bad line\n2 not-a-number 5\n3 8 not-a-size\n\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	resume, err = GetResume(dir)
	if err != nil || !reflect.DeepEqual(resume.ChunksDone, []ChunkComp{second, first}) {
		t.Fatalf("loaded = %+v, %v", resume, err)
	}
	if resume.TotalEncodedSize() != 7 || resume.TotalEncodedFrames() != 30 || !resume.DoneSet()[0] || !resume.DoneSet()[1] {
		t.Fatalf("resume totals = %+v", resume)
	}
	if err := os.WriteFile(IVFPath(dir, 0), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(IVFPath(dir, 1), []byte("wrong"), 0600); err != nil {
		t.Fatal(err)
	}
	valid := resume.Validate(dir, []Chunk{{Idx: 0, Start: 0, End: 10}, {Idx: 1, Start: 10, End: 30}})
	if !reflect.DeepEqual(valid.ChunksDone, []ChunkComp{first}) {
		t.Fatalf("validated = %+v", valid)
	}
	if err := os.WriteFile(IVFPath(dir, 1), []byte("1234"), 0600); err != nil {
		t.Fatal(err)
	}
	valid = resume.Validate(dir, []Chunk{{Idx: 0, Start: 0, End: 10}, {Idx: 1, Start: 10, End: 30}})
	if !reflect.DeepEqual(valid.ChunksDone, []ChunkComp{first, second}) {
		t.Fatalf("validated sorted = %+v", valid)
	}
	if len(resume.Validate(dir, []Chunk{{Idx: 0, Start: 0, End: 9}}).ChunksDone) != 0 {
		t.Fatal("accepted wrong frame count or unknown chunk")
	}
	if err := AppendDone(first, filepath.Join(dir, "absent")); err == nil {
		t.Fatal("append to missing directory accepted")
	}
	if _, err := GetResume(dir + "/done.txt/nope"); err == nil {
		t.Fatal("invalid workdir accepted")
	}
}
