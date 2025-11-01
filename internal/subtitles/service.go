package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/media/audio"
	"spindle/internal/media/ffprobe"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/stage"
)

const (
	progressStageGenerating = "Generating AI subtitles"

	whisperXCommand        = "uvx"
	whisperXCUDAIndexURL   = "https://download.pytorch.org/whl/cu128"
	whisperXPypiIndexURL   = "https://pypi.org/simple"
	whisperXModel          = "large-v3"
	whisperXAlignModel     = "WAV2VEC2_ASR_LARGE_LV60K_960H"
	whisperXBatchSize      = "4"
	whisperXChunkSize      = "15"
	whisperXVADMethod      = "pyannote"
	whisperXVADOnset       = "0.08"
	whisperXVADOffset      = "0.07"
	whisperXBeamSize       = "10"
	whisperXBestOf         = "10"
	whisperXTemperature    = "0.0"
	whisperXPatience       = "1.0"
	whisperXSegmentRes     = "sentence"
	whisperXOutputFormat   = "all"
	whisperXCPUDevice      = "cpu"
	whisperXCUDADevice     = "cuda"
	whisperXCPUComputeType = "float32"
	netflixMaxLineChars    = 42
	lyricLineCharLimit     = 28
	netflixMaxLines        = 2
	netflixMaxCharsPerSec  = 17.0
	netflixMaxCueDuration  = 7 * time.Second
	netflixMinCueDuration  = 500 * time.Millisecond
	netflixMinCueGap       = 120 * time.Millisecond
	durationEpsilon        = 5 * time.Millisecond
)

var (
	inspectMedia = ffprobe.Inspect
)

type commandRunner func(ctx context.Context, name string, args ...string) error

// GenerateRequest describes the inputs for subtitle generation.
type GenerateRequest struct {
	SourcePath string
	WorkDir    string
	OutputDir  string
	Language   string
	BaseName   string
}

// GenerateResult reports the generated subtitle file and summary stats.
type GenerateResult struct {
	SubtitlePath string
	SegmentCount int
	Duration     time.Duration
}

// Service orchestrates WhisperX execution and Netflix-compliant subtitle output.
type Service struct {
	config *config.Config
	logger *slog.Logger
	run    commandRunner
}

// ServiceOption customizes a Service.
type ServiceOption func(*Service)

// WithCommandRunner injects a custom command runner (primarily for tests).
func WithCommandRunner(r commandRunner) ServiceOption {
	return func(s *Service) {
		if r != nil {
			s.run = r
		}
	}
}

// NewService constructs a subtitle generation service.
func NewService(cfg *config.Config, logger *slog.Logger, opts ...ServiceOption) *Service {
	serviceLogger := logger
	if serviceLogger != nil {
		serviceLogger = serviceLogger.With(logging.String("component", "subtitles"))
	}
	svc := &Service{
		config: cfg,
		logger: serviceLogger,
		run:    defaultCommandRunner,
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// Generate produces an SRT file for the provided source.
func (s *Service) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	if s == nil {
		return GenerateResult{}, services.Wrap(services.ErrConfiguration, "subtitles", "init", "Subtitle service unavailable", nil)
	}

	source := strings.TrimSpace(req.SourcePath)
	if source == "" {
		return GenerateResult{}, services.Wrap(services.ErrValidation, "subtitles", "validate input", "Source path is empty", nil)
	}
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return GenerateResult{}, services.Wrap(services.ErrNotFound, "subtitles", "stat source", "Source file not found", err)
		}
		return GenerateResult{}, services.Wrap(services.ErrValidation, "subtitles", "stat source", "Failed to inspect source file", err)
	}

	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		if req.OutputDir != "" {
			workDir = req.OutputDir
		} else {
			workDir = filepath.Dir(source)
		}
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return GenerateResult{}, services.Wrap(services.ErrConfiguration, "subtitles", "ensure workdir", "Failed to create subtitle work directory", err)
	}

	outputDir := strings.TrimSpace(req.OutputDir)
	if outputDir == "" {
		outputDir = filepath.Dir(source)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return GenerateResult{}, services.Wrap(services.ErrConfiguration, "subtitles", "ensure output", "Failed to create subtitle output directory", err)
	}

	probe, err := inspectMedia(ctx, s.ffprobeBinary(), source)
	if err != nil {
		return GenerateResult{}, services.Wrap(services.ErrExternalTool, "subtitles", "ffprobe", "Failed to probe media", err)
	}
	selection := audio.Select(probe.Streams)
	if selection.PrimaryIndex < 0 {
		return GenerateResult{}, services.Wrap(services.ErrValidation, "subtitles", "select audio", "No primary audio track available for subtitles", nil)
	}
	if s.logger != nil {
		s.logger.Info("selected primary audio stream",
			logging.String("codec", selection.Primary.CodecName),
			logging.Int("index", selection.Primary.Index),
		)
	}

	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = inferLanguage(selection.Primary.Tags)
	}
	if language == "" {
		language = "en"
	}
	baseName := strings.TrimSpace(req.BaseName)
	if baseName == "" {
		filename := filepath.Base(source)
		baseName = strings.TrimSuffix(filename, filepath.Ext(filename))
	}

	runDir := filepath.Join(workDir, "whisperx")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return GenerateResult{}, services.Wrap(services.ErrConfiguration, "subtitles", "ensure whisperx dir", "Failed to create WhisperX directory", err)
	}
	defer func() {
		_ = os.RemoveAll(runDir)
	}()

	sourceBase := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	args := s.buildWhisperXArgs(source, runDir, language)
	if s.logger != nil {
		s.logger.Info("running whisperx",
			logging.String("model", whisperXModel),
			logging.String("align_model", whisperXAlignModel),
			logging.String("language", language),
			logging.Bool("cuda", s.config != nil && s.config.WhisperXCUDAEnabled),
		)
	}
	if err := s.run(ctx, whisperXCommand, args...); err != nil {
		return GenerateResult{}, services.Wrap(services.ErrExternalTool, "subtitles", "whisperx", "WhisperX execution failed", err)
	}

	jsonPath := filepath.Join(runDir, sourceBase+".json")
	segments, err := loadWhisperSegments(jsonPath)
	if err != nil {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "parse whisperx", "Failed to parse WhisperX output", err)
	}
	cueGroups := buildCueWordGroups(segments)
	if len(cueGroups) == 0 {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "whisperx output", "WhisperX returned no aligned words", nil)
	}

	cues := convertCueWordsToCues(cueGroups)
	if len(cues) == 0 {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "format", "No cues produced after Netflix formatting", nil)
	}
	cues = enforceCueDurations(cues)

	outputFile := filepath.Join(outputDir, fmt.Sprintf("%s.%s.srt", baseName, language))
	if err := writeSRT(outputFile, cues); err != nil {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "write srt", "Failed to write subtitle file", err)
	}

	totalDuration := probe.DurationSeconds()
	if totalDuration <= 0 {
		totalDuration = cues[len(cues)-1].End
	}

	if s.logger != nil {
		s.logger.Info("subtitle generation complete",
			logging.String("output", outputFile),
			logging.Int("segments", len(cues)),
			logging.Float64("duration_seconds", totalDuration),
		)
	}

	result := GenerateResult{
		SubtitlePath: outputFile,
		SegmentCount: len(cues),
		Duration:     time.Duration(totalDuration * float64(time.Second)),
	}
	return result, nil
}

func (s *Service) ffprobeBinary() string {
	if s != nil && s.config != nil {
		return s.config.FFprobeBinary()
	}
	return "ffprobe"
}

func (s *Service) buildWhisperXArgs(source, outputDir, language string) []string {
	cudaEnabled := s != nil && s.config != nil && s.config.WhisperXCUDAEnabled

	args := make([]string, 0, 32)
	if cudaEnabled {
		args = append(args,
			"--index-url", whisperXCUDAIndexURL,
			"--extra-index-url", whisperXPypiIndexURL,
		)
	} else {
		args = append(args,
			"--index-url", whisperXPypiIndexURL,
		)
	}

	args = append(args,
		"whisperx",
		source,
		"--model", whisperXModel,
		"--align_model", whisperXAlignModel,
		"--batch_size", whisperXBatchSize,
		"--output_dir", outputDir,
		"--output_format", whisperXOutputFormat,
		"--segment_resolution", whisperXSegmentRes,
		"--chunk_size", whisperXChunkSize,
		"--vad_method", whisperXVADMethod,
		"--vad_onset", whisperXVADOnset,
		"--vad_offset", whisperXVADOffset,
		"--beam_size", whisperXBeamSize,
		"--best_of", whisperXBestOf,
		"--temperature", whisperXTemperature,
		"--patience", whisperXPatience,
	)

	if lang := normalizeWhisperLanguage(language); lang != "" {
		args = append(args, "--language", lang)
	}
	if cudaEnabled {
		args = append(args, "--device", whisperXCUDADevice)
	} else {
		args = append(args, "--device", whisperXCPUDevice, "--compute_type", whisperXCPUComputeType)
	}
	// Ensure highlight_words is disabled (default false) without passing CLI flag.
	return args
}

type whisperJSON struct {
	Segments []whisperSegment `json:"segments"`
}

type whisperSegment struct {
	Start float64           `json:"start"`
	End   float64           `json:"end"`
	Text  string            `json:"text"`
	Words []whisperWordJSON `json:"words"`
}

type whisperWordJSON struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type word struct {
	Text  string
	Start float64
	End   float64
}

func loadWhisperSegments(path string) ([]whisperSegment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read whisperx json: %w", err)
	}
	var parsed whisperJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode whisperx json: %w", err)
	}
	return parsed.Segments, nil
}

type cueWordGroup struct {
	Words   []word
	IsLyric bool
}

func buildCueWordGroups(segments []whisperSegment) []cueWordGroup {
	groups := make([]cueWordGroup, 0, len(segments))
	for _, seg := range segments {
		words := segmentWords(seg)
		if len(words) == 0 {
			continue
		}
		isLyric := isLikelyLyricSegment(seg, words)
		groups = append(groups, splitWordsIntoCues(words, isLyric)...)
	}
	groups = mergeShortCueWords(groups)
	groups = promoteLyricClusters(groups)
	return mergeLyricGroups(groups)
}

func segmentWords(seg whisperSegment) []word {
	if len(seg.Words) == 0 {
		text := normalizeWhitespace(seg.Text)
		if text == "" || seg.End <= seg.Start {
			return nil
		}
		return []word{{Text: text, Start: seg.Start, End: seg.End}}
	}
	words := make([]word, 0, len(seg.Words))
	for _, w := range seg.Words {
		text := normalizeWhitespace(w.Word)
		if text == "" {
			continue
		}
		start := w.Start
		end := w.End
		if end <= start {
			end = start + 0.05
		}
		words = append(words, word{Text: text, Start: start, End: end})
	}
	return words
}

func splitWordsIntoCues(words []word, isLyric bool) []cueWordGroup {
	if len(words) == 0 {
		return nil
	}
	current := make([]word, 0, len(words))
	cues := make([]cueWordGroup, 0, len(words)/3+1)

	for _, w := range words {
		for {
			candidate := append(copyWords(current), w)
			if violatesNetflixRules(candidate) {
				if len(current) == 0 {
					current = candidate
					break
				}
				if idx := findSplitIndex(current); idx >= 0 {
					cues = append(cues, cueWordGroup{Words: copyWords(current[:idx+1]), IsLyric: isLyric})
					current = copyWords(current[idx+1:])
					continue
				}
				cues = append(cues, cueWordGroup{Words: copyWords(current), IsLyric: isLyric})
				current = current[:0]
				continue
			}
			current = candidate
			break
		}
	}
	if len(current) > 0 {
		cues = append(cues, cueWordGroup{Words: copyWords(current), IsLyric: isLyric})
	}
	return cues
}

func findSplitIndex(words []word) int {
	if len(words) == 0 {
		return -1
	}
	punct := ".!?…;:,—-\""
	for i := len(words) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(words[i].Text)
		if trimmed == "" {
			continue
		}
		runes := []rune(trimmed)
		if strings.ContainsRune(punct, runes[len(runes)-1]) {
			return i
		}
	}
	if len(words) > 1 {
		return len(words)/2 - 1
	}
	return -1
}

func mergeShortCueWords(groups []cueWordGroup) []cueWordGroup {
	if len(groups) == 0 {
		return groups
	}
	result := make([]cueWordGroup, 0, len(groups))
	for i := 0; i < len(groups); i++ {
		current := groups[i]
		for i+1 < len(groups) && current.IsLyric == groups[i+1].IsLyric && shouldMergeWithNext(current, groups[i+1]) {
			candidateWords := append(copyWords(current.Words), groups[i+1].Words...)
			if violatesNetflixRules(candidateWords) {
				break
			}
			current = cueWordGroup{Words: candidateWords, IsLyric: current.IsLyric}
			i++
		}
		result = append(result, current)
	}
	return result
}

func shouldMergeWithNext(current, next cueWordGroup) bool {
	if len(current.Words) == 0 || len(next.Words) == 0 {
		return false
	}
	if current.IsLyric {
		return true
	}
	if len(current.Words) <= 2 || cueDuration(current.Words) < 0.8 {
		return true
	}
	lastText := strings.TrimSpace(current.Words[len(current.Words)-1].Text)
	if lastText == "" {
		return true
	}
	lastRune := []rune(lastText)[len([]rune(lastText))-1]
	if strings.ContainsRune(".!?…", lastRune) {
		return false
	}
	return len(current.Words) <= 6
}

func cueDuration(words []word) float64 {
	if len(words) == 0 {
		return 0
	}
	return words[len(words)-1].End - words[0].Start
}

func convertCueWordsToCues(groups []cueWordGroup) []Cue {
	if len(groups) == 0 {
		return nil
	}
	cues := make([]Cue, 0, len(groups))
	for _, group := range groups {
		if len(group.Words) == 0 {
			continue
		}
		var lines []string
		if group.IsLyric {
			lines = wrapLyricText(joinWords(group.Words))
		} else {
			lines = wrapText(joinWords(group.Words))
		}
		if len(lines) == 0 {
			continue
		}
		start := group.Words[0].Start
		end := group.Words[len(group.Words)-1].End
		if end <= start {
			end = start + 0.1
		}
		var text string
		if group.IsLyric {
			text = formatLyric(lines)
		} else {
			text = strings.Join(lines, "\n")
		}
		cues = append(cues, Cue{Start: start, End: end, Text: text})
	}
	return cues
}

func copyWords(words []word) []word {
	result := make([]word, len(words))
	copy(result, words)
	return result
}

func violatesNetflixRules(words []word) bool {
	if len(words) == 0 {
		return false
	}
	text := joinWords(words)
	lines := wrapText(text)
	if len(lines) > netflixMaxLines {
		return true
	}
	for _, line := range lines {
		if utf8.RuneCountInString(line) > netflixMaxLineChars {
			return true
		}
	}
	start := words[0].Start
	end := words[len(words)-1].End
	if end-start > netflixMaxCueDuration.Seconds()+durationEpsilon.Seconds() {
		return true
	}
	duration := end - start
	if duration <= 0 {
		duration = 0.1
	}
	charCount := float64(countCharacters(strings.Join(lines, "")))
	if duration >= 1.0 {
		ratio := charCount / duration
		if ratio > netflixMaxCharsPerSec {
			return true
		}
	}
	return false
}

func enforceCueDurations(cues []Cue) []Cue {
	if len(cues) == 0 {
		return cues
	}
	for i := range cues {
		if cues[i].End <= cues[i].Start {
			cues[i].End = cues[i].Start + 0.1
		}
		duration := cues[i].End - cues[i].Start
		if duration > netflixMaxCueDuration.Seconds() {
			cues[i].End = cues[i].Start + netflixMaxCueDuration.Seconds()
		}
	}
	for i := range cues {
		duration := cues[i].End - cues[i].Start
		if duration < netflixMinCueDuration.Seconds() {
			desiredEnd := cues[i].Start + netflixMinCueDuration.Seconds()
			if i+1 < len(cues) {
				limit := cues[i+1].Start - netflixMinCueGap.Seconds()
				if desiredEnd > limit {
					desiredEnd = math.Max(limit, cues[i].Start+0.1)
				}
			}
			cues[i].End = math.Min(desiredEnd, cues[i].Start+netflixMaxCueDuration.Seconds())
		}
	}
	for i := 1; i < len(cues); i++ {
		minStart := cues[i-1].End + netflixMinCueGap.Seconds()
		if cues[i].Start < minStart {
			shift := minStart - cues[i].Start
			cues[i].Start += shift
			if cues[i].End <= cues[i].Start {
				cues[i].End = cues[i].Start + 0.1
			}
		}
	}
	return cues
}

func joinWords(words []word) string {
	if len(words) == 0 {
		return ""
	}
	tokens := make([]string, 0, len(words))
	for _, w := range words {
		tokens = append(tokens, sanitizeWord(w.Text))
	}
	return normalizeWhitespace(strings.Join(tokens, " "))
}

func sanitizeWord(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
}

func normalizeWhitespace(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	fields := strings.Fields(text)
	return strings.Join(fields, " ")
}

func wrapText(text string) []string {
	return wrapTextWithLimit(text, netflixMaxLineChars)
}

func wrapLyricText(text string) []string {
	lines := wrapTextWithLimit(text, lyricLineCharLimit)
	if len(lines) > 2 {
		lines = lines[:2]
	}
	return lines
}

func wrapTextWithLimit(text string, limit int) []string {
	if text == "" {
		return nil
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, 2)
	current := ""
	for _, token := range words {
		parts := splitLongWord(token, limit)
		for _, part := range parts {
			if current == "" {
				current = part
				continue
			}
			if utf8.RuneCountInString(current)+1+utf8.RuneCountInString(part) <= limit {
				current = current + " " + part
				continue
			}
			lines = append(lines, current)
			current = part
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func isLikelyLyricSegment(seg whisperSegment, words []word) bool {
	if len(words) == 0 {
		return false
	}
	text := strings.TrimSpace(seg.Text)
	if text == "" {
		text = joinWords(words)
	}
	if strings.ContainsAny(text, ".!?") {
		return false
	}
	letters := 0
	other := 0
	for _, r := range text {
		switch {
		case unicode.IsLetter(r):
			letters++
		case unicode.IsSpace(r), r == '\'', r == ',':
		default:
			other++
		}
	}
	if other > 0 {
		return false
	}
	totalDuration := words[len(words)-1].End - words[0].Start
	if totalDuration < 0.4 {
		return false
	}
	average := totalDuration / float64(len(words))
	return average >= 0.45
}

func mergeLyricGroups(groups []cueWordGroup) []cueWordGroup {
	if len(groups) == 0 {
		return groups
	}
	result := make([]cueWordGroup, 0, len(groups))
	current := groups[0]
	for i := 1; i < len(groups); i++ {
		next := groups[i]
		if current.IsLyric && next.IsLyric {
			gap := next.Words[0].Start - current.Words[len(current.Words)-1].End
			if gap <= 1.5 {
				combined := append(copyWords(current.Words), next.Words...)
				if !violatesNetflixRules(combined) {
					current.Words = combined
					continue
				}
			}
		}
		result = append(result, current)
		current = next
	}
	result = append(result, current)
	return result
}

func promoteLyricClusters(groups []cueWordGroup) []cueWordGroup {
	if len(groups) == 0 {
		return groups
	}
	result := make([]cueWordGroup, 0, len(groups))
	for i := 0; i < len(groups); {
		if isLyricFragment(groups[i]) {
			j := i + 1
			combined := copyWords(groups[i].Words)
			for j < len(groups) && isLyricFragment(groups[j]) {
				candidate := append(copyWords(combined), groups[j].Words...)
				if cueDuration(candidate) > 6.5 {
					break
				}
				combined = candidate
				j++
			}
			if j-i > 1 {
				result = append(result, cueWordGroup{Words: combined, IsLyric: true})
				i = j
				continue
			}
		}
		if groups[i].IsLyric {
			j := i + 1
			combined := copyWords(groups[i].Words)
			merged := false
			for j < len(groups) && isLyricFragment(groups[j]) {
				candidate := append(copyWords(combined), groups[j].Words...)
				if cueDuration(candidate) > 6.5 {
					break
				}
				combined = candidate
				merged = true
				j++
			}
			if merged {
				result = append(result, cueWordGroup{Words: combined, IsLyric: true})
				i = j
				continue
			}
		}
		if isLyricFragment(groups[i]) && len(result) > 0 && result[len(result)-1].IsLyric {
			prev := &result[len(result)-1]
			gap := groups[i].Words[0].Start - prev.Words[len(prev.Words)-1].End
			candidate := append(copyWords(prev.Words), groups[i].Words...)
			if gap <= 1.5 && cueDuration(candidate) <= 6.5 {
				prev.Words = candidate
				i++
				continue
			}
		}
		result = append(result, groups[i])
		i++
	}
	return result
}

func isLyricFragment(group cueWordGroup) bool {
	if len(group.Words) == 0 {
		return false
	}
	if cueDuration(group.Words) > 1.6 {
		return false
	}
	if len(group.Words) > 6 {
		return false
	}
	text := joinWords(group.Words)
	trimmed := strings.TrimRight(text, ",.!?…")
	if trimmed == "" {
		trimmed = text
	}
	if strings.ContainsAny(trimmed, ".!?…") {
		return false
	}
	return true
}

func formatLyric(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
		if lines[i] == "" {
			continue
		}
		lines[i] = normalizeLyricLine(lines[i])
	}
	note := "\u266A"
	lines[0] = fmt.Sprintf("%s %s", note, lines[0])
	if len(lines) == 1 {
		lines[0] = strings.TrimSpace(lines[0]) + " " + note
	} else {
		last := len(lines) - 1
		lines[last] = strings.TrimSpace(lines[last]) + " " + note
	}
	return "<i>" + strings.Join(lines, "\n") + "</i>"
}

func normalizeLyricLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return line
	}
	runes := []rune(line)
	// Uppercase first rune if letter
	first := runes[0]
	if unicode.IsLetter(first) {
		runes[0] = unicode.ToUpper(first)
	}
	// remove forbidden end punctuation
	for len(runes) > 0 {
		last := runes[len(runes)-1]
		if last == '!' || last == '?' {
			break
		}
		if last == '.' || last == ',' || last == ';' || last == ':' {
			runes = runes[:len(runes)-1]
			continue
		}
		break
	}
	return strings.TrimSpace(string(runes))
}

func splitLongWord(word string, limit int) []string {
	if utf8.RuneCountInString(word) <= limit {
		return []string{word}
	}
	runes := []rune(word)
	var parts []string
	for len(runes) > limit {
		parts = append(parts, string(runes[:limit]))
		runes = runes[limit:]
	}
	if len(runes) > 0 {
		parts = append(parts, string(runes))
	}
	return parts
}

func countCharacters(text string) int {
	return utf8.RuneCountInString(strings.ReplaceAll(text, "\n", ""))
}

func normalizeWhisperLanguage(language string) string {
	lang := strings.TrimSpace(strings.ToLower(language))
	if len(lang) == 2 {
		return lang
	}
	if len(lang) == 3 {
		switch lang {
		case "eng":
			return "en"
		case "spa":
			return "es"
		case "fra":
			return "fr"
		case "ger", "deu":
			return "de"
		case "ita":
			return "it"
		case "por":
			return "pt"
		case "dut", "nld":
			return "nl"
		case "rus":
			return "ru"
		case "jpn":
			return "ja"
		case "kor":
			return "ko"
		}
	}
	return ""
}

func defaultCommandRunner(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec
	var stderr strings.Builder
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Stage integrates subtitle generation with the workflow manager.
type Stage struct {
	store   *queue.Store
	service *Service
	logger  *slog.Logger
}

// NewStage constructs a workflow stage that generates subtitles for queue items.
func NewStage(store *queue.Store, service *Service, logger *slog.Logger) *Stage {
	stageLogger := logger
	if stageLogger != nil {
		stageLogger = stageLogger.With(logging.String("component", "subtitle-stage"))
	}
	return &Stage{store: store, service: service, logger: stageLogger}
}

// Prepare primes queue progress fields before executing the stage.
func (s *Stage) Prepare(ctx context.Context, item *queue.Item) error {
	if s == nil || s.service == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "prepare", "Subtitle stage is not configured", nil)
	}
	if !s.service.config.SubtitlesEnabled {
		return nil
	}
	if s.store == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "prepare", "Queue store unavailable", nil)
	}
	item.ProgressStage = progressStageGenerating
	item.ProgressMessage = "Preparing audio for transcription"
	item.ProgressPercent = 0
	item.ErrorMessage = ""
	return s.store.UpdateProgress(ctx, item)
}

// Execute performs subtitle generation for the queue item.
func (s *Stage) Execute(ctx context.Context, item *queue.Item) error {
	if s == nil || s.service == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "execute", "Subtitle stage is not configured", nil)
	}
	if item == nil {
		return services.Wrap(services.ErrValidation, "subtitles", "execute", "Queue item is nil", nil)
	}
	if s.store == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "execute", "Queue store unavailable", nil)
	}
	if strings.TrimSpace(item.EncodedFile) == "" {
		return services.Wrap(services.ErrValidation, "subtitles", "execute", "No encoded file available for subtitles", nil)
	}
	if !s.service.config.SubtitlesEnabled {
		return nil
	}

	if err := s.updateProgress(ctx, item, "Running WhisperX transcription", 5); err != nil {
		return err
	}
	workDir := item.StagingRoot(s.service.config.StagingDir)
	outputDir := filepath.Dir(strings.TrimSpace(item.EncodedFile))
	result, err := s.service.Generate(ctx, GenerateRequest{
		SourcePath: item.EncodedFile,
		WorkDir:    filepath.Join(workDir, "subtitles"),
		OutputDir:  outputDir,
		BaseName:   baseNameWithoutExt(item.EncodedFile),
	})
	if err != nil {
		return err
	}
	item.ProgressMessage = fmt.Sprintf("Generated subtitles: %s", filepath.Base(result.SubtitlePath))
	item.ProgressPercent = 100
	if err := s.store.UpdateProgress(ctx, item); err != nil {
		return services.Wrap(services.ErrTransient, "subtitles", "persist progress", "Failed to persist subtitle progress", err)
	}
	return nil
}

func (s *Stage) updateProgress(ctx context.Context, item *queue.Item, message string, percent float64) error {
	item.ProgressStage = progressStageGenerating
	if strings.TrimSpace(message) != "" {
		item.ProgressMessage = message
	}
	if percent >= 0 {
		item.ProgressPercent = percent
	}
	if err := s.store.UpdateProgress(ctx, item); err != nil {
		return services.Wrap(services.ErrTransient, "subtitles", "persist progress", "Failed to persist subtitle progress", err)
	}
	return nil
}

// HealthCheck reports readiness for the subtitle stage.
func (s *Stage) HealthCheck(ctx context.Context) stage.Health {
	if s == nil || s.service == nil {
		return stage.Unhealthy("subtitles", "stage not configured")
	}
	if !s.service.config.SubtitlesEnabled {
		return stage.Healthy("subtitles")
	}
	return stage.Healthy("subtitles")
}

func baseNameWithoutExt(path string) string {
	filename := filepath.Base(strings.TrimSpace(path))
	if filename == "" {
		return "subtitle"
	}
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

// Cue represents a single subtitle cue in seconds.
type Cue struct {
	Start float64
	End   float64
	Text  string
}

func writeSRT(path string, cues []Cue) error {
	var b strings.Builder
	for i, cue := range cues {
		start := formatSRTTimestamp(cue.Start)
		end := formatSRTTimestamp(cue.End)
		if _, err := fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, start, end, cue.Text); err != nil {
			return fmt.Errorf("write cue %d: %w", i+1, err)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func formatSRTTimestamp(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	totalMillis := int(math.Round(seconds * 1000))
	hours := totalMillis / (60 * 60 * 1000)
	remainder := totalMillis % (60 * 60 * 1000)
	minutes := remainder / (60 * 1000)
	remainder %= 60 * 1000
	secs := remainder / 1000
	millis := remainder % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, secs, millis)
}

func inferLanguage(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := []string{"language", "LANGUAGE", "lang", "LANG"}
	for _, key := range keys {
		if value, ok := tags[key]; ok {
			value = strings.TrimSpace(strings.ReplaceAll(value, "\u0000", ""))
			if value != "" {
				return strings.ToLower(value)
			}
		}
	}
	return ""
}
