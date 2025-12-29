package subtitles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"spindle/internal/logging"
	"spindle/internal/media/audio"
	"spindle/internal/services"
)

type generationPlan struct {
	source       string
	workDir      string
	outputDir    string
	runDir       string
	audioPath    string
	whisperSRT   string
	whisperJSON  string
	outputFile   string
	language     string
	totalSeconds float64
	audioIndex   int
	cudaEnabled  bool
	cleanup      func()
}

func (s *Service) prepareGenerationPlan(ctx context.Context, req GenerateRequest) (*generationPlan, error) {
	source := strings.TrimSpace(req.SourcePath)
	if source == "" {
		return nil, services.Wrap(services.ErrValidation, "subtitles", "validate input", "Source path is empty", nil)
	}
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, services.Wrap(services.ErrNotFound, "subtitles", "stat source", "Source file not found", err)
		}
		return nil, services.Wrap(services.ErrValidation, "subtitles", "stat source", "Failed to inspect source file", err)
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
		return nil, services.Wrap(services.ErrConfiguration, "subtitles", "ensure workdir", "Failed to create subtitle work directory", err)
	}

	outputDir := strings.TrimSpace(req.OutputDir)
	if outputDir == "" {
		outputDir = filepath.Dir(source)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, services.Wrap(services.ErrConfiguration, "subtitles", "ensure output", "Failed to create subtitle output directory", err)
	}

	probe, err := inspectMedia(ctx, s.ffprobeBinary(), source)
	if err != nil {
		return nil, services.Wrap(services.ErrExternalTool, "subtitles", "ffprobe", "Failed to probe media", err)
	}

	selection := audio.Select(probe.Streams)
	if selection.PrimaryIndex < 0 {
		return nil, services.Wrap(services.ErrValidation, "subtitles", "select audio", "No primary audio track available for subtitles", nil)
	}
	if s.logger != nil {
		s.logger.Debug("selected primary audio stream",
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
		return nil, services.Wrap(services.ErrConfiguration, "subtitles", "ensure whisperx dir", "Failed to create WhisperX directory", err)
	}

	cleanup := func() {}
	if os.Getenv("SPD_DEBUG_SUBTITLES_KEEP") == "" {
		cleanup = func() {
			_ = os.RemoveAll(runDir)
		}
	}

	audioPath := filepath.Join(runDir, "primary_audio.wav")
	audioBase := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))

	return &generationPlan{
		source:       source,
		workDir:      workDir,
		outputDir:    outputDir,
		runDir:       runDir,
		audioPath:    audioPath,
		whisperSRT:   filepath.Join(runDir, audioBase+".srt"),
		whisperJSON:  filepath.Join(runDir, audioBase+".json"),
		outputFile:   filepath.Join(outputDir, fmt.Sprintf("%s.%s.srt", baseName, language)),
		language:     language,
		totalSeconds: probe.DurationSeconds(),
		audioIndex:   selection.PrimaryIndex,
		cudaEnabled:  s != nil && s.config != nil && s.config.Subtitles.WhisperXCUDAEnabled,
		cleanup:      cleanup,
	}, nil
}

func (s *Service) invokeWhisperX(ctx context.Context, plan *generationPlan) error {
	if plan == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "whisperx", "Subtitle generation plan not initialized", nil)
	}

	args := s.buildWhisperXArgs(plan.audioPath, plan.runDir, plan.language)
	if s.logger != nil {
		s.logger.Debug("running whisperx",
			logging.String("model", whisperXModel),
			logging.String("align_model", whisperXAlignModel),
			logging.String("language", plan.language),
			logging.Bool("cuda", plan.cudaEnabled),
		)
	}
	if err := s.run(ctx, whisperXCommand, args...); err != nil {
		return services.Wrap(services.ErrExternalTool, "subtitles", "whisperx", "WhisperX execution failed", err)
	}

	if _, err := os.Stat(plan.whisperSRT); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return services.Wrap(services.ErrTransient, "subtitles", "whisperx output", "WhisperX did not produce an SRT file", err)
		}
		return services.Wrap(services.ErrTransient, "subtitles", "whisperx output", "Failed to inspect WhisperX SRT", err)
	}

	return nil
}
