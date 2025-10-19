package subtitles

import (
	"bytes"
	"context"
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
	defaultChunkDuration    = 12 * time.Minute
	defaultChunkOverlap     = 2 * time.Second
	progressStageGenerating = "Generating AI subtitles"
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

// Service orchestrates audio extraction, transcription, and SRT generation.
type Service struct {
	config        *config.Config
	client        Client
	logger        *slog.Logger
	chunkDuration time.Duration
	chunkOverlap  time.Duration
	run           commandRunner
}

// ServiceOption customizes a Service.
type ServiceOption func(*Service)

// WithChunkDuration overrides the per-request chunk duration.
func WithChunkDuration(d time.Duration) ServiceOption {
	return func(s *Service) {
		if d > 0 {
			s.chunkDuration = d
		}
	}
}

// WithChunkOverlap overrides the inter-chunk overlap duration.
func WithChunkOverlap(d time.Duration) ServiceOption {
	return func(s *Service) {
		if d >= 0 {
			s.chunkOverlap = d
		}
	}
}

// WithCommandRunner injects a custom command runner (primarily for tests).
func WithCommandRunner(r commandRunner) ServiceOption {
	return func(s *Service) {
		if r != nil {
			s.run = r
		}
	}
}

// NewService constructs a subtitle generation service.
func NewService(cfg *config.Config, client Client, logger *slog.Logger, opts ...ServiceOption) *Service {
	serviceLogger := logger
	if serviceLogger != nil {
		serviceLogger = serviceLogger.With(logging.String("component", "subtitles"))
	}
	svc := &Service{
		config:        cfg,
		client:        client,
		logger:        serviceLogger,
		chunkDuration: defaultChunkDuration,
		chunkOverlap:  defaultChunkOverlap,
		run:           defaultCommandRunner,
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
	if s.client == nil {
		return GenerateResult{}, services.Wrap(services.ErrConfiguration, "subtitles", "init", "No transcription client configured", nil)
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

	outputFile := filepath.Join(outputDir, fmt.Sprintf("%s.%s.srt", baseName, language))
	plan := chooseDemuxPlan()

	totalDuration := probe.DurationSeconds()

	chunks, err := s.buildChunks(totalDuration)
	if err != nil {
		return GenerateResult{}, err
	}

	cues := make([]Cue, 0, len(chunks)*8)
	chunkDir := filepath.Join(workDir, "chunks")
	if err := os.MkdirAll(chunkDir, 0o755); err != nil {
		return GenerateResult{}, services.Wrap(services.ErrConfiguration, "subtitles", "ensure chunk dir", "Failed to create subtitle chunk directory", err)
	}
	defer func() {
		_ = os.RemoveAll(chunkDir)
	}()

	for idx, chunk := range chunks {
		target := filepath.Join(chunkDir, chunkFileName(baseName, chunk.Index, plan.Extension))
		total := len(chunks)
		if s.logger != nil {
			s.logger.Info("transcoding audio chunk",
				logging.Int("chunk", idx+1),
				logging.Int("total_chunks", total),
				logging.Float64("start_seconds", chunk.Start),
				logging.Float64("end_seconds", chunk.End),
				logging.String("output", target),
			)
		}
		if err := s.encodeChunk(ctx, source, selection.PrimaryIndex, plan, chunk.Start, chunk.End, target); err != nil {
			return GenerateResult{}, err
		}
		if s.logger != nil {
			s.logger.Info("uploading chunk to mistral",
				logging.Int("chunk", idx+1),
				logging.Int("total_chunks", total),
				logging.String("file", target),
			)
		}
		resp, err := s.client.Transcribe(ctx, TranscriptionRequest{
			FilePath:    target,
			Language:    "",
			Model:       defaultMistralModel,
			Granularity: granularitySegment,
		})
		_ = os.Remove(target)
		if err != nil {
			if s.logger != nil {
				s.logger.Error("mistral chunk request failed",
					logging.Int("chunk", idx+1),
					logging.Int("total_chunks", total),
					logging.Error(err),
				)
			}
			return GenerateResult{}, services.Wrap(services.ErrExternalTool, "subtitles", "transcribe", "Mistral transcription failed", err)
		}
		if s.logger != nil {
			s.logger.Info("chunk transcription completed",
				logging.Int("chunk", idx+1),
				logging.Int("total_chunks", total),
				logging.Int("segment_count", len(resp.Segments)),
			)
		}
		offset := chunk.Start
		base := chunk.Base
		for _, span := range resp.Segments {
			adjStart := offset + span.Start
			adjEnd := offset + span.End
			if adjEnd <= base+0.05 {
				continue
			}
			if adjStart < base {
				adjStart = base
			}
			cues = appendCue(cues, Cue{Start: adjStart, End: adjEnd, Text: span.Text})
		}
	}

	if len(cues) == 0 {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "transcribe", "Transcription returned no segments", nil)
	}

	if err := writeSRT(outputFile, cues); err != nil {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "write srt", "Failed to write subtitle file", err)
	}
	if s.logger != nil {
		s.logger.Info("subtitle generation complete",
			logging.String("output", outputFile),
			logging.Int("segments", len(cues)),
			logging.Float64("duration_seconds", totalDuration),
		)
	}

	result := GenerateResult{SubtitlePath: outputFile, SegmentCount: len(cues), Duration: time.Duration(totalDuration * float64(time.Second))}
	return result, nil
}

func (s *Service) ffprobeBinary() string {
	if s != nil && s.config != nil {
		return s.config.FFprobeBinary()
	}
	return "ffprobe"
}

func (s *Service) encodeChunk(ctx context.Context, source string, streamIndex int, plan demuxPlan, start, end float64, target string) error {
	duration := end - start
	if duration <= 0 {
		duration = 0.5
	}
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-ss", formatFFmpegTime(start),
		"-i", source,
		"-map", fmt.Sprintf("0:%d", streamIndex),
		"-vn",
		"-sn",
		"-dn",
		"-t", formatFFmpegTime(duration),
		"-c:a", plan.AudioCodec,
	}
	if len(plan.ExtraArgs) > 0 {
		args = append(args, plan.ExtraArgs...)
	}
	args = append(args, "-af", "asetpts=PTS-STARTPTS")
	if plan.Format != "" {
		args = append(args, "-f", plan.Format)
	}
	args = append(args, target)
	if err := s.run(ctx, "ffmpeg", args...); err != nil {
		return services.Wrap(services.ErrExternalTool, "subtitles", "transcode chunk", "ffmpeg chunk transcode failed", err)
	}
	return nil
}

type audioChunk struct {
	Index int
	Start float64
	End   float64
	Base  float64
}

type demuxPlan struct {
	Extension  string
	Format     string
	AudioCodec string
	ExtraArgs  []string
}

func chooseDemuxPlan() demuxPlan {
	return demuxPlan{
		Extension:  ".opus",
		Format:     "opus",
		AudioCodec: "libopus",
		ExtraArgs:  []string{"-ac", "1", "-ar", "16000", "-b:a", "64000"},
	}
}

func (s *Service) buildChunks(totalDuration float64) ([]audioChunk, error) {
	if s.chunkDuration <= 0 {
		return nil, services.Wrap(services.ErrConfiguration, "subtitles", "chunk setup", "Chunk duration must be positive", nil)
	}
	chunks := make([]audioChunk, 0)
	chunkSeconds := s.chunkDuration.Seconds()
	if chunkSeconds <= 0 {
		chunkSeconds = defaultChunkDuration.Seconds()
	}
	if totalDuration <= 0 {
		// Treat as one chunk when duration is unavailable.
		chunks = append(chunks, audioChunk{Index: 0, Start: 0, End: chunkSeconds, Base: 0})
		return chunks, nil
	}
	total := totalDuration
	overlap := math.Max(0, s.chunkOverlap.Seconds())

	for index, start := 0, 0.0; start < total; index, start = index+1, start+chunkSeconds {
		chunkBase := start
		chunkStart := chunkBase
		if index > 0 && overlap > 0 {
			chunkStart = math.Max(0, chunkBase-overlap)
		}
		chunkEnd := math.Min(total, chunkBase+chunkSeconds)
		if chunkEnd-chunkStart <= 0.1 {
			break
		}
		chunks = append(chunks, audioChunk{Index: index, Start: chunkStart, End: chunkEnd, Base: chunkBase})
		if chunkEnd >= total {
			break
		}
	}
	if len(chunks) == 0 {
		chunks = append(chunks, audioChunk{Index: 0, Start: 0, End: total, Base: 0})
	}
	return chunks, nil
}

func chunkFileName(base string, index int, ext string) string {
	trimmedExt := strings.TrimSpace(ext)
	if trimmedExt == "" {
		trimmedExt = ".opus"
	}
	return fmt.Sprintf("%s.chunk.%03d%s", base, index, trimmedExt)
}

func formatFFmpegTime(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	return fmt.Sprintf("%.3f", seconds)
}

func inferLanguage(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := []string{"language", "LANGUAGE", "lang", "LANG"}
	for _, key := range keys {
		if value, ok := tags[key]; ok {
			value = strings.TrimSpace(value)
			value = strings.ReplaceAll(value, "\u0000", "")
			if value != "" {
				return strings.ToLower(value)
			}
		}
	}
	return ""
}

func defaultCommandRunner(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec
	var stderr bytes.Buffer
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
	if strings.TrimSpace(s.service.config.MistralAPIKey) == "" {
		return services.Wrap(services.ErrConfiguration, "subtitles", "execute", "Subtitles enabled but mistral_api_key not set", nil)
	}

	if err := s.updateProgress(ctx, item, "Extracting primary audio", 5); err != nil {
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
	if strings.TrimSpace(s.service.config.MistralAPIKey) == "" {
		return stage.Unhealthy("subtitles", "mistral_api_key missing")
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

// appendCue normalizes and appends a cue with basic deduplication heuristics.
func appendCue(cues []Cue, cue Cue) []Cue {
	cue.Text = normalizeText(cue.Text)
	if cue.Text == "" {
		return cues
	}
	if cue.Start < 0 {
		cue.Start = 0
	}
	if cue.End <= cue.Start {
		cue.End = cue.Start + 0.1
	}
	if len(cues) > 0 {
		prev := cues[len(cues)-1]
		if merged, ok := mergeCue(prev, cue); ok {
			cues[len(cues)-1] = merged
			return cues
		}
		if cue.Start < prev.End {
			trim := math.Max(prev.Start, cue.Start-0.04)
			prev.End = math.Max(prev.Start, trim)
			cues[len(cues)-1] = prev
		}
	}
	return append(cues, cue)
}

func mergeCue(prev Cue, next Cue) (Cue, bool) {
	if !timingAllowsMerge(prev, next) {
		return Cue{}, false
	}
	if !textShouldMerge(prev.Text, next.Text) {
		return Cue{}, false
	}
	if next.End > prev.End {
		prev.End = next.End
	}
	if preferNextText(prev.Text, next.Text) {
		prev.Text = next.Text
	}
	return prev, true
}

func textShouldMerge(prevText, nextText string) bool {
	prevKey := compareKey(prevText)
	nextKey := compareKey(nextText)
	if prevKey == "" || nextKey == "" {
		return false
	}
	if prevKey == nextKey {
		return true
	}
	if strings.Contains(nextKey, prevKey) || strings.Contains(prevKey, nextKey) {
		return true
	}
	return false
}

func preferNextText(prevText, nextText string) bool {
	prevKey := compareKey(prevText)
	nextKey := compareKey(nextText)
	if prevKey == "" || nextKey == "" {
		return false
	}
	if nextKey == prevKey {
		return false
	}
	if strings.Contains(nextKey, prevKey) {
		return true
	}
	return false
}

func timingAllowsMerge(prev Cue, next Cue) bool {
	startGap := math.Abs(next.Start - prev.Start)
	if startGap < 0.15 {
		return true
	}
	if next.Start >= prev.End {
		gap := next.Start - prev.End
		if gap <= 0.3 {
			return true
		}
	}
	if next.Start < prev.End {
		overlap := prev.End - next.Start
		if overlap <= 0.3 {
			return true
		}
	}
	return false
}

func compareKey(text string) string {
	clean := normalizeText(text)
	if clean == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range clean {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r):
			b.WriteRune(unicode.ToLower(r))
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// writeSRT writes cues to disk in SRT format.
func writeSRT(path string, cues []Cue) error {
	if len(cues) == 0 {
		return fmt.Errorf("write srt: no cues provided")
	}
	var b strings.Builder
	for idx, cue := range cues {
		if idx > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("%d\n", idx+1))
		b.WriteString(fmt.Sprintf("%s --> %s\n", formatTimestamp(cue.Start), formatTimestamp(cue.End)))
		for _, line := range wrapText(cue.Text, 42) {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func formatTimestamp(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	ms := int(math.Round(seconds * 1000))
	hours := ms / (3600 * 1000)
	minutes := (ms / (60 * 1000)) % 60
	secondsPart := (ms / 1000) % 60
	millis := ms % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, secondsPart, millis)
}

func wrapText(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	if width <= 0 {
		width = 42
	}
	lines := make([]string, 0)
	var current strings.Builder
	for _, word := range words {
		if current.Len() == 0 {
			current.WriteString(word)
			continue
		}
		if current.Len()+1+len(word) <= width {
			current.WriteString(" ")
			current.WriteString(word)
			continue
		}
		lines = append(lines, current.String())
		current.Reset()
		current.WriteString(word)
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

func normalizeText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\n", " ")
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	return trimmed
}
