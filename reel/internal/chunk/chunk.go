// Package chunk provides types and functions for managing video encoding chunks.
package chunk

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Segment represents a planned content segment in the video.
type Segment struct {
	StartFrame int
	EndFrame   int
}

// Chunk represents a video chunk for encoding.
type Chunk struct {
	Idx   int // Chunk index (0-based)
	Start int // Start frame (inclusive)
	End   int // End frame (exclusive)
}

// Frames returns the number of frames in this chunk.
func (c Chunk) Frames() int {
	return c.End - c.Start
}

// ChunkComp represents a completed chunk's information.
type ChunkComp struct {
	Idx    int    // Chunk index
	Frames int    // Number of frames encoded
	Size   uint64 // Output file size in bytes
}

// ResumeInf contains information for resuming an interrupted encode.
type ResumeInf struct {
	ChunksDone []ChunkComp
}

// ResumeManifest records the source and encode settings for safe resume.
type ResumeManifest struct {
	Version               int     `json:"version"`
	InputPath             string  `json:"input_path"`
	InputSize             int64   `json:"input_size"`
	InputModTimeUnixNano  int64   `json:"input_mod_time_unix_nano"`
	Width                 uint32  `json:"width"`
	Height                uint32  `json:"height"`
	FPSNum                uint32  `json:"fps_num"`
	FPSDen                uint32  `json:"fps_den"`
	Frames                int     `json:"frames"`
	CropFilter            string  `json:"crop_filter,omitempty"`
	QualityMode           string  `json:"quality_mode,omitempty"`
	Quality               float32 `json:"quality"`
	TargetQuality         string  `json:"target_quality,omitempty"`
	CRFSearchRange        string  `json:"crf_search_range,omitempty"`
	Preset                uint8   `json:"preset"`
	Tune                  uint8   `json:"tune"`
	ACBias                float32 `json:"ac_bias"`
	EnableVarianceBoost   bool    `json:"enable_variance_boost"`
	VarianceBoostStrength uint8   `json:"variance_boost_strength"`
	VarianceOctile        uint8   `json:"variance_octile"`
	GrainTreatment        string  `json:"grain_treatment,omitempty"`
	Denoise               string  `json:"denoise,omitempty"`
	GrainTable            string  `json:"fgs_table,omitempty"`
	ChunkDurationSecs     float64 `json:"chunk_duration_secs"`
	ChunkFingerprint      string  `json:"chunk_fingerprint"`
}

// LoadSegments loads planned segment boundaries from a file.
// The file format is one frame number per line.
func LoadSegments(path string, totalFrames int) ([]Segment, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open segment boundary file: %w", err)
	}
	defer func() { _ = file.Close() }()

	var frameNums []int
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		frameNum, err := strconv.Atoi(line)
		if err != nil {
			return nil, fmt.Errorf("invalid frame number %q: %w", line, err)
		}
		frameNums = append(frameNums, frameNum)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading segment boundary file: %w", err)
	}

	// Sort frame numbers
	sort.Ints(frameNums)

	// Ensure we start at frame 0
	if len(frameNums) == 0 || frameNums[0] != 0 {
		frameNums = append([]int{0}, frameNums...)
	}

	// Convert to planned segments
	segments := make([]Segment, 0, len(frameNums))
	for i := 0; i < len(frameNums); i++ {
		start := frameNums[i]
		end := totalFrames
		if i+1 < len(frameNums) {
			end = frameNums[i+1]
		}

		if start < end {
			segments = append(segments, Segment{
				StartFrame: start,
				EndFrame:   end,
			})
		}
	}

	return segments, nil
}

// ValidateSegments checks that segments are valid and not too long.
func ValidateSegments(segments []Segment, fpsNum, fpsDen uint32) error {
	if len(segments) == 0 {
		return fmt.Errorf("no segments provided")
	}

	// Validate FPS denominator to prevent division by zero
	if fpsDen == 0 {
		return fmt.Errorf("invalid FPS denominator: 0")
	}

	// Calculate max segment length (30 seconds or 1000 frames, whichever is smaller)
	fps := float64(fpsNum) / float64(fpsDen)
	maxFrames := min(int(fps*30), 1000)

	for i, segment := range segments {
		length := segment.EndFrame - segment.StartFrame
		if length > maxFrames {
			return fmt.Errorf("segment %d is too long: %d frames (max %d)", i, length, maxFrames)
		}
		if length <= 0 {
			return fmt.Errorf("segment %d has invalid length: %d", i, length)
		}
	}

	return nil
}

// Chunkify converts planned segments to chunks for encoding.
// Each segment becomes one chunk.
func Chunkify(segments []Segment) []Chunk {
	chunks := make([]Chunk, len(segments))
	for i, segment := range segments {
		chunks[i] = Chunk{
			Idx:   i,
			Start: segment.StartFrame,
			End:   segment.EndFrame,
		}
	}
	return chunks
}

// GetResume loads resume information from the work directory.
func GetResume(workDir string) (*ResumeInf, error) {
	donePath := filepath.Join(workDir, "done.txt")

	file, err := os.Open(donePath)
	if os.IsNotExist(err) {
		return &ResumeInf{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open resume file: %w", err)
	}
	defer func() { _ = file.Close() }()

	var chunks []ChunkComp
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue // Skip malformed lines
		}

		idx, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}

		frames, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}

		size, err := strconv.ParseUint(parts[2], 10, 64)
		if err != nil {
			continue
		}

		chunks = append(chunks, ChunkComp{
			Idx:    idx,
			Frames: frames,
			Size:   size,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading resume file: %w", err)
	}

	return &ResumeInf{ChunksDone: chunks}, nil
}

// AppendDone appends a completed chunk to the resume file.
func AppendDone(chunk ChunkComp, workDir string) error {
	donePath := filepath.Join(workDir, "done.txt")

	file, err := os.OpenFile(donePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open resume file: %w", err)
	}
	defer func() { _ = file.Close() }()

	_, err = fmt.Fprintf(file, "%d %d %d\n", chunk.Idx, chunk.Frames, chunk.Size)
	if err != nil {
		return fmt.Errorf("failed to append resume data: %w", err)
	}

	return nil
}

// Validate returns resume information only for chunks whose output files still exist and match done.txt.
func (r *ResumeInf) Validate(workDir string, chunks []Chunk) *ResumeInf {
	expectedFrames := make(map[int]int, len(chunks))
	for _, ch := range chunks {
		expectedFrames[ch.Idx] = ch.Frames()
	}

	valid := make(map[int]ChunkComp, len(r.ChunksDone))
	for _, c := range r.ChunksDone {
		expected, ok := expectedFrames[c.Idx]
		if !ok || expected != c.Frames || !validChunkFile(workDir, c) {
			continue
		}
		valid[c.Idx] = c
	}

	chunksDone := make([]ChunkComp, 0, len(valid))
	for _, c := range valid {
		chunksDone = append(chunksDone, c)
	}
	sort.Slice(chunksDone, func(i, j int) bool { return chunksDone[i].Idx < chunksDone[j].Idx })
	return &ResumeInf{ChunksDone: chunksDone}
}

func validChunkFile(workDir string, c ChunkComp) bool {
	stat, err := os.Stat(IVFPath(workDir, c.Idx))
	if err != nil || stat.Size() <= 0 {
		return false
	}
	return c.Size == uint64(stat.Size())
}

// DoneSet returns a set of completed chunk indices for quick lookup.
func (r *ResumeInf) DoneSet() map[int]bool {
	done := make(map[int]bool, len(r.ChunksDone))
	for _, c := range r.ChunksDone {
		done[c.Idx] = true
	}
	return done
}

// TotalEncodedSize returns the total size of all completed chunks.
func (r *ResumeInf) TotalEncodedSize() uint64 {
	var total uint64
	for _, c := range r.ChunksDone {
		total += c.Size
	}
	return total
}

// TotalEncodedFrames returns the total frames of all completed chunks.
func (r *ResumeInf) TotalEncodedFrames() int {
	var total int
	for _, c := range r.ChunksDone {
		total += c.Frames
	}
	return total
}

// IVFPath returns the path to a chunk's IVF file.
func IVFPath(workDir string, chunkIdx int) string {
	return filepath.Join(workDir, "encode", fmt.Sprintf("%04d.ivf", chunkIdx))
}

// EnsureEncodeDir ensures the encode directory exists.
func EnsureEncodeDir(workDir string) error {
	encodeDir := filepath.Join(workDir, "encode")
	return os.MkdirAll(encodeDir, 0755)
}

// EnsureResumeManifest saves or validates the resume manifest for a work
// directory. A mismatched or unreadable manifest means the input or encode
// settings changed since the interrupted run (a re-ripped source, an
// upgraded reel): the stale resume state is discarded and the encode starts
// over instead of refusing until the directory is removed by hand. Returns
// whether stale state was discarded.
func EnsureResumeManifest(workDir string, manifest ResumeManifest) (bool, error) {
	manifest.Version = 1
	path := filepath.Join(workDir, "resume.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, writeResumeManifest(path, manifest)
	}
	if err != nil {
		return false, fmt.Errorf("failed to read resume manifest: %w", err)
	}

	var existing ResumeManifest
	if unmarshalErr := json.Unmarshal(data, &existing); unmarshalErr == nil && reflect.DeepEqual(existing, manifest) {
		return false, nil
	}
	if err := resetResumeState(workDir); err != nil {
		return false, err
	}
	return true, writeResumeManifest(path, manifest)
}

// resetResumeState removes everything in workDir except the chunk plan,
// which self-validates against the input and chunking options (see
// chunkplan.PlanToFileIfNeeded) and is therefore already fresh or still
// correct whenever the resume manifest is not.
func resetResumeState(workDir string) error {
	keep := map[string]bool{"chunk-plan.txt": true, "chunk-plan.json": true}
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return fmt.Errorf("failed to reset resume state: %w", err)
	}
	for _, entry := range entries {
		if keep[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(workDir, entry.Name())); err != nil {
			return fmt.Errorf("failed to reset resume state: %w", err)
		}
	}
	return nil
}

// WriteResumeManifest overwrites the manifest without the staleness check.
// The chunked pipeline uses it to pin a decision (the grain gate's verdict)
// that is only known after EnsureResumeManifest has validated the directory,
// while nothing has been encoded yet.
func WriteResumeManifest(workDir string, manifest ResumeManifest) error {
	manifest.Version = 1
	return writeResumeManifest(filepath.Join(workDir, "resume.json"), manifest)
}

func writeResumeManifest(path string, manifest ResumeManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode resume manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write resume manifest: %w", err)
	}
	return nil
}

// ChunkFingerprint returns a stable fingerprint for the chunk boundaries.
func ChunkFingerprint(chunks []Chunk) string {
	h := sha256.New()
	for _, ch := range chunks {
		_, _ = fmt.Fprintf(h, "%d:%d:%d\n", ch.Idx, ch.Start, ch.End)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// CanonicalInputPath returns the absolute, symlink-resolved path for an input when possible.
func CanonicalInputPath(inputPath string) string {
	abs, err := filepath.Abs(inputPath)
	if err != nil {
		abs = inputPath
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved
	}
	return abs
}

// WorkDirName generates a work directory name from the canonical input path.
func WorkDirName(inputPath string) string {
	canonical := CanonicalInputPath(inputPath)
	base := filepath.Base(canonical)
	ext := filepath.Ext(base)
	name := base[:len(base)-len(ext)]
	sum := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf(".reel-%s-%s", name, hex.EncodeToString(sum[:])[:12])
}
