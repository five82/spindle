package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/audioanalysis"
	"spindle/internal/config"
	"spindle/internal/deps"
	langpkg "spindle/internal/language"
	"spindle/internal/media/audio"
	"spindle/internal/media/ffprobe"
	"spindle/internal/services/llm"
	"spindle/internal/services/whisperx"
	"spindle/internal/textutil"
)

func newCacheCommentaryCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "commentary <entry|path>",
		Short: "Run commentary detection on a cached rip file",
		Long: `Run commentary detection on a cached rip file.

Provide either a cache entry number (from 'spindle cache stats') or a direct path
to a rip cache file/directory. This command is useful for troubleshooting and
tuning commentary detection settings.

The command will:
1. Find English 2-channel stereo audio tracks as commentary candidates
2. Transcribe the primary audio and each candidate using WhisperX
3. Compare transcripts to filter out stereo downmixes (similarity check)
4. Classify remaining candidates with the LLM
5. Save transcripts to text files for review and troubleshooting

Example:
  spindle cache commentary 1        # Run on cache entry #1
  spindle cache commentary /path/to/file.mkv`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return err
			}

			target, label, err := resolveCommentaryTarget(ctx, args[0], cmd.OutOrStdout())
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			start := time.Now()
			fmt.Fprintf(out, "Commentary Detection Start: %s\n", start.Format("Jan 2 2006 15:04:05 MST"))

			result, err := runCommentaryDetection(cmd.Context(), cfg, target, out)

			end := time.Now()
			fmt.Fprintf(out, "Commentary Detection End: %s\n", end.Format("Jan 2 2006 15:04:05 MST"))
			fmt.Fprintf(out, "Commentary Detection Duration: %s\n", end.Sub(start).Round(time.Millisecond))

			if err != nil {
				return err
			}

			fmt.Fprintf(out, "\nTarget: %s\n", label)
			printCommentaryResults(out, result)

			if err := writeCommentaryTranscripts(result, out); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}

// commentaryDetectionResult captures the full results of commentary detection.
type commentaryDetectionResult struct {
	PrimaryIndex        int
	PrimaryLabel        string
	PrimaryTranscript   string
	Candidates          []candidateResult
	CommentaryIndices   []int
	SimilarityThreshold float64
	ConfidenceThreshold float64
}

// candidateResult captures the detection result for a single candidate track.
type candidateResult struct {
	Index        int
	Language     string
	Title        string
	Channels     int
	Transcript   string
	Similarity   float64
	IsDownmix    bool
	LLMDecision  *audioanalysis.CommentaryDecision
	IsCommentary bool
	Reason       string
}

func resolveCommentaryTarget(ctx *commandContext, arg string, out io.Writer) (string, string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", errors.New("cache entry is required")
	}

	if entryNum, err := strconv.Atoi(arg); err == nil {
		if entryNum < 1 {
			return "", "", fmt.Errorf("invalid cache entry number: %d", entryNum)
		}
		manager, warn, err := cacheManager(ctx)
		if warn != "" {
			fmt.Fprintln(out, warn)
		}
		if err != nil || manager == nil {
			if err != nil {
				return "", "", err
			}
			return "", "", errors.New("rip cache is unavailable")
		}
		stats, err := manager.Stats(context.Background())
		if err != nil {
			return "", "", err
		}
		if entryNum > len(stats.EntrySummaries) {
			return "", "", fmt.Errorf("cache entry %d out of range (only %d entries exist)", entryNum, len(stats.EntrySummaries))
		}
		entry := stats.EntrySummaries[entryNum-1]
		if entry.PrimaryFile == "" {
			return "", "", fmt.Errorf("cache entry %d has no detectable video files", entryNum)
		}
		target := filepath.Join(entry.Directory, entry.PrimaryFile)
		label := strings.TrimSpace(entry.PrimaryFile)
		if label == "" {
			label = filepath.Base(entry.Directory)
		}
		return target, label, nil
	}

	path, err := config.ExpandPath(arg)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("inspect path %q: %w", path, err)
	}
	if info.IsDir() {
		target, count, err := selectPrimaryVideo(path)
		if err != nil {
			return "", "", err
		}
		label := filepath.Base(path)
		if label == "" {
			label = path
		}
		if count > 1 {
			label = fmt.Sprintf("%s (+%d more)", label, count-1)
		}
		return target, label, nil
	}
	return path, path, nil
}

func selectPrimaryVideo(dir string) (string, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, fmt.Errorf("read cache directory %q: %w", dir, err)
	}
	type candidate struct {
		name string
		size int64
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".mkv" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{name: entry.Name(), size: info.Size()})
	}
	if len(candidates) == 0 {
		return "", 0, fmt.Errorf("no video files found in %q", dir)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].size == candidates[j].size {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].size > candidates[j].size
	})
	return filepath.Join(dir, candidates[0].name), len(candidates), nil
}

func runCommentaryDetection(ctx context.Context, cfg *config.Config, target string, out io.Writer) (*commentaryDetectionResult, error) {
	// Probe the target file
	ffprobeBinary := deps.ResolveFFprobePath(cfg.FFprobeBinary())
	probe, err := ffprobe.Inspect(ctx, ffprobeBinary, target)
	if err != nil {
		return nil, fmt.Errorf("ffprobe inspect: %w", err)
	}

	// Select primary audio
	selection := audio.Select(probe.Streams)
	if selection.PrimaryIndex < 0 {
		return nil, errors.New("no audio streams found")
	}

	result := &commentaryDetectionResult{
		PrimaryIndex:        selection.PrimaryIndex,
		PrimaryLabel:        selection.PrimaryLabel(),
		SimilarityThreshold: cfg.Commentary.SimilarityThreshold,
		ConfidenceThreshold: cfg.Commentary.ConfidenceThreshold,
	}

	// Find commentary candidates
	candidates := findCommentaryCandidates(probe.Streams, selection.PrimaryIndex)
	if len(candidates) == 0 {
		fmt.Fprintln(out, "No commentary candidates found (no 2-channel English/unknown tracks)")
		return result, nil
	}

	fmt.Fprintf(out, "Found %d commentary candidate(s)\n", len(candidates))

	// Set up working directory
	workDir, err := os.MkdirTemp("", "spindle-commentary-*")
	if err != nil {
		return nil, fmt.Errorf("create temp directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	// Initialize WhisperX service
	whisperCfg := whisperx.Config{
		Model:       cfg.Commentary.WhisperXModel,
		CUDAEnabled: cfg.Subtitles.WhisperXCUDAEnabled,
		VADMethod:   cfg.Subtitles.WhisperXVADMethod,
		HFToken:     cfg.Subtitles.WhisperXHuggingFace,
	}
	if whisperCfg.Model == "" {
		whisperCfg.Model = cfg.Subtitles.WhisperXModel
	}
	whisperSvc := whisperx.NewService(whisperCfg, deps.ResolveFFmpegPath())

	fmt.Fprintf(out, "Transcribing primary audio (track #%d)...\n", selection.PrimaryIndex)
	primaryDir := filepath.Join(workDir, "primary")
	primaryTranscript, err := transcribeSegment(ctx, whisperSvc, target, selection.PrimaryIndex, primaryDir)
	if err != nil {
		return nil, fmt.Errorf("transcribe primary audio: %w", err)
	}
	result.PrimaryTranscript = primaryTranscript
	primaryFingerprint := textutil.NewFingerprint(primaryTranscript)

	// Create LLM client if configured
	var llmClient *llm.Client
	llmCfg := cfg.CommentaryLLM()
	if llmCfg.APIKey != "" {
		llmClient = llm.NewClient(llm.Config{
			APIKey:  llmCfg.APIKey,
			BaseURL: llmCfg.BaseURL,
			Model:   llmCfg.Model,
			Referer: llmCfg.Referer,
			Title:   llmCfg.Title,
		})
	}

	// Process each candidate
	for _, stream := range candidates {
		fmt.Fprintf(out, "Processing candidate track #%d...\n", stream.Index)

		candResult := candidateResult{
			Index:    stream.Index,
			Language: langpkg.ExtractFromTags(stream.Tags),
			Title:    streamTitle(stream.Tags),
			Channels: stream.Channels,
		}

		// Transcribe candidate
		candDir := filepath.Join(workDir, fmt.Sprintf("candidate-%d", stream.Index))
		candidateTranscript, err := transcribeSegment(ctx, whisperSvc, target, stream.Index, candDir)
		if err != nil {
			candResult.Reason = fmt.Sprintf("transcription failed: %v", err)
			result.Candidates = append(result.Candidates, candResult)
			continue
		}
		candResult.Transcript = candidateTranscript

		candidateFingerprint := textutil.NewFingerprint(candidateTranscript)

		// Check similarity to primary audio
		similarity := textutil.CosineSimilarity(primaryFingerprint, candidateFingerprint)
		candResult.Similarity = similarity

		if similarity >= cfg.Commentary.SimilarityThreshold {
			candResult.IsDownmix = true
			candResult.Reason = "stereo_downmix"
			result.Candidates = append(result.Candidates, candResult)
			continue
		}

		// Classify with LLM
		if llmClient != nil {
			decision, err := classifyWithLLM(ctx, llmClient, candidateTranscript, target)
			if err != nil {
				candResult.Reason = fmt.Sprintf("LLM classification failed: %v", err)
				result.Candidates = append(result.Candidates, candResult)
				continue
			}

			candResult.LLMDecision = &decision
			candResult.IsCommentary = decision.IsCommentary(cfg.Commentary.ConfidenceThreshold)
			if candResult.IsCommentary {
				candResult.Reason = "llm_accepted"
				result.CommentaryIndices = append(result.CommentaryIndices, stream.Index)
			} else {
				candResult.Reason = "llm_rejected"
			}
		} else {
			candResult.Reason = "llm_not_configured"
		}

		result.Candidates = append(result.Candidates, candResult)
	}

	return result, nil
}

// findCommentaryCandidates finds English/unknown 2-channel stereo tracks that could be commentary.
func findCommentaryCandidates(streams []ffprobe.Stream, primaryIndex int) []ffprobe.Stream {
	var candidates []ffprobe.Stream
	for _, stream := range streams {
		if !isCommentaryCandidate(stream, primaryIndex) {
			continue
		}
		candidates = append(candidates, stream)
	}
	return candidates
}

// isCommentaryCandidate returns true if the stream could be a commentary track.
func isCommentaryCandidate(stream ffprobe.Stream, primaryIndex int) bool {
	if stream.CodecType != "audio" || stream.Index == primaryIndex || stream.Channels != 2 {
		return false
	}
	lang := langpkg.ExtractFromTags(stream.Tags)
	return strings.HasPrefix(lang, "en") || lang == "" || lang == "und"
}

func transcribeSegment(ctx context.Context, whisperSvc *whisperx.Service, sourcePath string, audioIndex int, workDir string) (string, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", fmt.Errorf("create segment dir: %w", err)
	}

	// Extract and transcribe first 10 minutes
	const segmentDurationSec = 600
	result, err := whisperSvc.TranscribeSegment(ctx, sourcePath, audioIndex, 0, segmentDurationSec, workDir, "en")
	if err != nil {
		return "", err
	}

	return result.Text, nil
}

func classifyWithLLM(ctx context.Context, client *llm.Client, transcript, targetPath string) (audioanalysis.CommentaryDecision, error) {
	title := filepath.Base(targetPath)
	userMessage := audioanalysis.BuildClassificationPrompt(title, "", transcript)

	response, err := client.CompleteJSON(ctx, audioanalysis.CommentaryClassificationPrompt, userMessage)
	if err != nil {
		return audioanalysis.CommentaryDecision{}, fmt.Errorf("llm completion: %w", err)
	}

	var decision audioanalysis.CommentaryDecision
	if err := llm.DecodeLLMJSON(response, &decision); err != nil {
		return audioanalysis.CommentaryDecision{}, fmt.Errorf("parse llm response: %w", err)
	}
	return decision, nil
}

func streamTitle(tags map[string]string) string {
	if tags == nil {
		return ""
	}
	for _, key := range []string{"title", "TITLE", "handler_name"} {
		if v, ok := tags[key]; ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func printCommentaryResults(out io.Writer, result *commentaryDetectionResult) {
	fmt.Fprintf(out, "Primary Audio: #%d %s\n", result.PrimaryIndex, result.PrimaryLabel)
	fmt.Fprintf(out, "Similarity Threshold: %.2f\n", result.SimilarityThreshold)
	fmt.Fprintf(out, "Confidence Threshold: %.2f\n", result.ConfidenceThreshold)

	if len(result.CommentaryIndices) == 0 {
		fmt.Fprintln(out, "Commentary Indices: none")
	} else {
		fmt.Fprintf(out, "Commentary Indices: %v\n", result.CommentaryIndices)
	}

	if len(result.Candidates) == 0 {
		fmt.Fprintln(out, "\nNo candidates processed.")
		return
	}

	fmt.Fprintln(out, "\nCandidate Details:")
	for _, cand := range result.Candidates {
		tag := "skip"
		if cand.IsCommentary {
			tag = "COMMENTARY"
		}

		lang := cand.Language
		if lang == "" {
			lang = "und"
		}

		title := cand.Title
		if title == "" {
			title = "(no title)"
		}

		fmt.Fprintf(out, "  #%d [%s] %s\n", cand.Index, tag, title)
		fmt.Fprintf(out, "      Language: %s, Channels: %d\n", lang, cand.Channels)
		fmt.Fprintf(out, "      Similarity: %.3f", cand.Similarity)
		if cand.IsDownmix {
			fmt.Fprintf(out, " (downmix detected)")
		}
		fmt.Fprintln(out)

		if cand.LLMDecision != nil {
			fmt.Fprintf(out, "      LLM Decision: %s (confidence: %.2f)\n",
				cand.LLMDecision.Decision, cand.LLMDecision.Confidence)
			if cand.LLMDecision.Reason != "" {
				fmt.Fprintf(out, "      LLM Reason: %s\n", cand.LLMDecision.Reason)
			}
		}
		fmt.Fprintf(out, "      Result: %s\n", cand.Reason)
	}
}

func writeCommentaryTranscripts(result *commentaryDetectionResult, out io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}

	fmt.Fprintln(out, "\nTranscripts (first 10 minutes):")

	// Write primary audio transcript
	if result.PrimaryTranscript != "" {
		filename := fmt.Sprintf("commentary_primary_%d.txt", result.PrimaryIndex)
		path := filepath.Join(cwd, filename)
		if err := os.WriteFile(path, []byte(result.PrimaryTranscript), 0o644); err != nil {
			return fmt.Errorf("write primary transcript: %w", err)
		}
		fmt.Fprintf(out, "  primary #%d -> %s\n", result.PrimaryIndex, filename)
	}

	// Write candidate transcripts
	for _, cand := range result.Candidates {
		if cand.Transcript == "" {
			continue
		}

		reason := cand.Reason
		if reason == "" {
			reason = "unknown"
		}

		filename := fmt.Sprintf("commentary_candidate_%d_%s.txt", cand.Index, sanitizeToken(reason))
		path := filepath.Join(cwd, filename)
		if err := os.WriteFile(path, []byte(cand.Transcript), 0o644); err != nil {
			return fmt.Errorf("write candidate transcript: %w", err)
		}
		fmt.Fprintf(out, "  candidate #%d -> %s\n", cand.Index, filename)
	}

	return nil
}

func sanitizeToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	out = strings.Trim(out, "_-")
	if out == "" {
		return "unknown"
	}
	return out
}
