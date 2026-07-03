package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/textutil"
	"github.com/five82/spindle/internal/transcription"
)

func newDebugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Diagnostic tools",
	}
	cmd.AddCommand(newDebugCropCmd(), newDebugCommentaryCmd())
	return cmd
}

func newDebugCropCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "crop <entry|path>",
		Short: "Run an ffmpeg cropdetect diagnostic on a video file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := resolveTarget(args[0])
			if err != nil {
				return err
			}
			ctx := context.Background()

			fmt.Printf("Running ffmpeg cropdetect on %s...\n", filepath.Base(path))
			result, err := detectDebugCrop(ctx, path)
			if err != nil {
				return fmt.Errorf("crop detection: %w", err)
			}

			fmt.Printf("\n%s\n", headerStyle("=== Crop Detection Results ==="))
			fmt.Printf("%s %dx%d\n", labelStyle("Resolution:    "), result.VideoWidth, result.VideoHeight)
			fmt.Printf("%s %v\n", labelStyle("HDR:           "), result.IsHDR)
			fmt.Printf("%s %v\n", labelStyle("Crop required: "), result.Required)
			if result.CropFilter != "" {
				fmt.Printf("%s %s\n", labelStyle("Crop filter:   "), result.CropFilter)
			}
			if result.MultipleRatios {
				fmt.Printf("%s yes\n", labelStyle("Multiple ratios:"))
			}
			fmt.Printf("%s %s\n", labelStyle("Message:       "), result.Message)
			fmt.Printf("%s %d\n", labelStyle("Total samples: "), result.TotalSamples)

			if len(result.Candidates) > 0 {
				fmt.Printf("\nCandidate distribution:\n")
				for _, c := range result.Candidates {
					fmt.Printf("  %-24s %3d samples (%.1f%%)\n", c.Crop, c.Count, c.Percent)
				}
			}

			return nil
		},
	}
}

type debugCropCandidate struct {
	Crop    string
	Count   int
	Percent float64
}

type debugCropResult struct {
	CropFilter     string
	Required       bool
	MultipleRatios bool
	Message        string
	Candidates     []debugCropCandidate
	TotalSamples   int
	VideoWidth     int
	VideoHeight    int
	IsHDR          bool
}

var debugCropRegex = regexp.MustCompile(`crop=(\d+:\d+:\d+:\d+)`)

func detectDebugCrop(ctx context.Context, path string) (*debugCropResult, error) {
	probeResult, err := ffprobe.Inspect(ctx, "", path)
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	var video ffprobe.Stream
	for _, stream := range probeResult.Streams {
		if stream.CodecType == "video" {
			video = stream
			break
		}
	}
	if video.Width == 0 || video.Height == 0 {
		return nil, fmt.Errorf("no video stream found")
	}

	isHDR := debugStreamIsHDR(video)
	threshold := 16
	if isHDR {
		threshold = 100
	}

	start := probeResult.DurationSeconds() * 0.5
	counts, err := sampleDebugCrop(ctx, path, start, threshold)
	if err != nil {
		return nil, err
	}

	result := analyzeDebugCropCounts(counts, video.Width, video.Height)
	result.VideoWidth = video.Width
	result.VideoHeight = video.Height
	result.IsHDR = isHDR
	return result, nil
}

func sampleDebugCrop(ctx context.Context, path string, start float64, threshold int) (map[string]int, error) {
	output, err := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-ss", fmt.Sprintf("%.2f", start),
		"-i", path,
		"-vframes", "10",
		"-vf", fmt.Sprintf("cropdetect=limit=%d:round=2:reset=1", threshold),
		"-f", "null",
		"-",
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg cropdetect: %w: %s", err, output)
	}

	counts := make(map[string]int)
	for _, matches := range debugCropRegex.FindAllStringSubmatch(string(output), -1) {
		counts[matches[1]]++
	}
	return counts, nil
}

func analyzeDebugCropCounts(counts map[string]int, width, height int) *debugCropResult {
	result := &debugCropResult{Message: "Analyzed 10 frames near midpoint"}
	if len(counts) == 0 {
		return result
	}

	candidates := make([]debugCropCandidate, 0, len(counts))
	for crop, count := range counts {
		result.TotalSamples += count
		candidates = append(candidates, debugCropCandidate{Crop: crop, Count: count})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Count == candidates[j].Count {
			return candidates[i].Crop < candidates[j].Crop
		}
		return candidates[i].Count > candidates[j].Count
	})
	for i := range candidates {
		candidates[i].Percent = float64(candidates[i].Count) / float64(result.TotalSamples) * 100
	}

	result.Candidates = candidates
	result.MultipleRatios = len(candidates) > 1
	result.CropFilter = "crop=" + candidates[0].Crop
	result.Required = debugCropRequired(candidates[0].Crop, width, height)
	if result.Required {
		result.Message = "Black bars detected"
	}
	return result
}

func debugCropRequired(crop string, sourceWidth, sourceHeight int) bool {
	cropWidth, cropHeight, err := encodingstate.ParseCropFilter(crop)
	if err != nil {
		return false
	}
	return cropWidth != sourceWidth || cropHeight != sourceHeight
}

func debugStreamIsHDR(stream ffprobe.Stream) bool {
	return strings.Contains(stream.ColorPrimaries, "2020") ||
		strings.Contains(stream.ColorSpace, "2020") ||
		stream.ColorTransfer == "smpte2084" ||
		stream.ColorTransfer == "arib-std-b67"
}

func newDebugCommentaryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "commentary <entry|path>",
		Short: "Run commentary detection on a video file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := resolveTarget(args[0])
			if err != nil {
				return err
			}
			ctx := context.Background()
			logger := buildLogger()

			fmt.Printf("Probing %s...\n", filepath.Base(path))
			probeResult, err := ffprobe.Inspect(ctx, "", path)
			if err != nil {
				return fmt.Errorf("ffprobe: %w", err)
			}

			// Collect audio streams.
			audioStreams := probeResult.AudioStreams()

			if len(audioStreams) == 0 {
				fmt.Println("No audio streams found")
				return nil
			}

			fmt.Printf("\n%s\n", headerStyle(fmt.Sprintf("=== Audio Streams (%d) ===", len(audioStreams))))
			for _, s := range audioStreams {
				title := s.Tags["title"]
				lang := s.Tags["language"]
				fmt.Printf("  Stream %d: %s, %d ch, %s", s.Index, s.CodecName, s.Channels, s.ChannelLayout)
				if lang != "" {
					fmt.Printf(", lang=%s", lang)
				}
				if title != "" {
					fmt.Printf(", title=%q", title)
				}
				fmt.Println()
			}

			if len(audioStreams) <= 1 {
				fmt.Println("\nOnly one audio stream; no commentary analysis needed")
				return nil
			}

			// Set up transcription and LLM for commentary detection.
			llmClient := llm.New(
				cfg.LLM.APIKey, cfg.LLM.BaseURL, cfg.LLM.Model,
				cfg.LLM.Referer, cfg.LLM.Title, cfg.LLM.TimeoutSeconds, nil,
			)
			if llmClient == nil {
				fmt.Println("\nLLM not configured; commentary classification requires LLM")
				return nil
			}

			transcriber := transcription.New(
				cfg.Commentary.WhisperXModel,
				cfg.Subtitles.WhisperXCUDAEnabled,
				cfg.Subtitles.WhisperXVADMethod,
				cfg.Subtitles.WhisperXHFToken,
				nil,
			)

			// Use a synthetic fingerprint for cache keys.
			debugFP := textutil.SanitizePathSegment(filepath.Base(path))

			fmt.Printf("\n%s\n", headerStyle("=== Commentary Analysis ==="))
			fmt.Printf("%s %.3f\n", labelStyle("Similarity threshold:"), cfg.Commentary.SimilarityThreshold)
			fmt.Printf("%s %.3f\n", labelStyle("Confidence threshold:"), cfg.Commentary.ConfidenceThreshold)

			// Use audio-relative indices for ffmpeg -map 0:a:N.
			for candidateAudioIdx, candidate := range audioStreams[1:] {
				candidateAudioIdx++ // 0-based: primary=0, first candidate=1
				fmt.Printf("\n%s\n", dimStyle(fmt.Sprintf("--- Stream %d ---", candidate.Index)))
				title := candidate.Tags["title"]
				if title != "" {
					fmt.Printf("%s %s\n", labelStyle("Title:   "), title)
				}
				fmt.Printf("%s %d (%s)\n", labelStyle("Channels:"), candidate.Channels, candidate.ChannelLayout)

				// Stereo similarity check.
				primaryResult, pErr := transcriber.Transcribe(ctx, transcription.TranscribeRequest{
					InputPath:  path,
					AudioIndex: 0,
					Language:   "en",
					OutputDir:  fmt.Sprintf("/tmp/spindle-debug-commentary-%s-0", debugFP),
				})
				if pErr != nil {
					logger.Warn("primary transcription failed", "error", pErr)
					fmt.Printf("Similarity: error (primary transcription failed)\n")
					continue
				}

				candidateResult, cErr := transcriber.Transcribe(ctx, transcription.TranscribeRequest{
					InputPath:  path,
					AudioIndex: candidateAudioIdx,
					Language:   "en",
					OutputDir:  fmt.Sprintf("/tmp/spindle-debug-commentary-%s-%d", debugFP, candidateAudioIdx),
				})
				if cErr != nil {
					logger.Warn("candidate transcription failed", "error", cErr)
					fmt.Printf("Similarity: error (candidate transcription failed)\n")
					continue
				}

				primaryText, _ := os.ReadFile(primaryResult.SRTPath)
				candidateText, _ := os.ReadFile(candidateResult.SRTPath)

				fpA := textutil.NewFingerprint(string(primaryText))
				fpB := textutil.NewFingerprint(string(candidateText))
				sim := textutil.CosineSimilarity(fpA, fpB)

				fmt.Printf("Similarity: %.3f", sim)
				if sim >= cfg.Commentary.SimilarityThreshold {
					fmt.Printf(" (>= %.3f, likely stereo downmix)\n", cfg.Commentary.SimilarityThreshold)
					continue
				}
				fmt.Println()

				// LLM classification.
				transcript := string(candidateText)
				if len(transcript) > 4000 {
					transcript = transcript[:4000] + "\n[truncated]"
				}

				var userPrompt strings.Builder
				if title != "" {
					fmt.Fprintf(&userPrompt, "Title: %s\n\n", title)
				}
				fmt.Fprintf(&userPrompt, "Transcript sample:\n%s", transcript)

				var resp struct {
					Decision   string  `json:"decision"`
					Confidence float64 `json:"confidence"`
					Reason     string  `json:"reason"`
				}
				if llmErr := llmClient.CompleteJSON(ctx, commentarySystemPrompt, userPrompt.String(), &resp); llmErr != nil {
					fmt.Printf("LLM: error (%v)\n", llmErr)
					continue
				}

				fmt.Printf("%s %s\n", labelStyle("LLM decision:  "), resp.Decision)
				fmt.Printf("%s %.2f\n", labelStyle("LLM confidence:"), resp.Confidence)
				fmt.Printf("%s %s\n", labelStyle("LLM reason:    "), resp.Reason)
			}

			return nil
		},
	}
}

// commentarySystemPrompt is the LLM system prompt for commentary classification.
const commentarySystemPrompt = `You are an assistant that determines if an audio track is commentary or not.

IMPORTANT: Commentary tracks come in two forms:
1. Commentary-only: People talking about the film without movie audio
2. Mixed commentary: Movie/TV dialogue plays while commentators talk over it

Both forms are commentary. The presence of movie dialogue does NOT mean it's not commentary.

Commentary tracks include:
- Director/cast commentary over the film
- Behind-the-scenes discussion mixed with film audio
- Any track where people discuss or react to the film while it plays

NOT commentary:
- Alternate language dubs
- Audio descriptions for visually impaired
- Stereo downmix of main audio
- Isolated music/effects tracks

Respond ONLY with JSON: {"decision": "commentary" or "not_commentary", "confidence": 0.0-1.0, "reason": "brief explanation"}`
