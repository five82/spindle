package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/audioanalysis"
	"spindle/internal/config"
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

			if err := audioanalysis.WriteDiagnosticTranscripts(result, out); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}

func resolveCommentaryTarget(ctx *commandContext, arg string, out io.Writer) (string, string, error) {
	return resolveCacheTarget(ctx, arg, out)
}

func runCommentaryDetection(ctx context.Context, cfg *config.Config, target string, out io.Writer) (*audioanalysis.DiagnosticResult, error) {
	return audioanalysis.RunDiagnostic(ctx, cfg, target, out)
}

func printCommentaryResults(out io.Writer, result *audioanalysis.DiagnosticResult) {
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
