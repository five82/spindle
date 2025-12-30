package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/config"
	"spindle/internal/deps"
	"spindle/internal/logging"
	"spindle/internal/media/audio"
	"spindle/internal/media/commentary"
	"spindle/internal/media/ffprobe"
)

func newCacheCommentaryCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "commentary <entry|path>",
		Short: "Run commentary detection on a cached rip file",
		Long: `Run commentary detection on a cached rip file.

Provide either a cache entry number (from 'spindle cache stats') or a direct path
to a rip cache file/directory.`,
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

			logLevel := ctx.resolvedLogLevel(cfg)
			logger, err := logging.New(logging.Options{
				Level:       logLevel,
				Format:      "console",
				Development: ctx.logDevelopment(cfg),
			})
			if err != nil {
				return fmt.Errorf("init logger: %w", err)
			}
			logger = logger.With(logging.String("component", "cli-commentary"))

			ffprobeBinary := deps.ResolveFFprobePath(cfg.FFprobeBinary())
			probe, err := ffprobe.Inspect(cmd.Context(), ffprobeBinary, target)
			if err != nil {
				return fmt.Errorf("ffprobe inspect: %w", err)
			}

			selection := audio.Select(probe.Streams)
			if selection.PrimaryIndex < 0 {
				return errors.New("no audio streams found for commentary detection")
			}

			result, err := commentary.Detect(cmd.Context(), cfg, target, probe, selection.PrimaryIndex, logger)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Target: %s\n", label)
			fmt.Fprintf(out, "Primary Audio: %s\n", selection.PrimaryLabel())
			if len(result.Indices) == 0 {
				fmt.Fprintln(out, "Commentary Indices: none")
			} else {
				fmt.Fprintf(out, "Commentary Indices: %v\n", result.Indices)
			}
			printCommentaryDecisions(out, result.Decisions)
			if err := writeCommentarySamples(cmd.Context(), cfg, target, result.Decisions, out); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
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

func printCommentaryDecisions(out io.Writer, decisions []commentary.Decision) {
	if len(decisions) == 0 {
		return
	}
	fmt.Fprintln(out, "Decisions:")
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].Index < decisions[j].Index })
	for _, decision := range decisions {
		tag := "skip"
		if decision.Include {
			tag = "keep"
		}
		meta := decision.Metadata
		reason := decision.Reason
		if reason == "" {
			reason = "unknown"
		}
		title := strings.TrimSpace(meta.Title)
		if title == "" {
			title = "(no title)"
		}
		fmt.Fprintf(out, "  - #%d (%s) %s — %s — %s\n", decision.Index, meta.Language, tag, reason, title)
	}
}

func writeCommentarySamples(ctx context.Context, cfg *config.Config, target string, decisions []commentary.Decision, out io.Writer) error {
	if len(decisions) == 0 {
		return nil
	}

	ffmpegBinary := deps.ResolveFFmpegPath(cfg.DraptoBinary())
	if strings.TrimSpace(ffmpegBinary) == "" {
		ffmpegBinary = "ffmpeg"
	}
	if _, err := exec.LookPath(ffmpegBinary); err != nil {
		return fmt.Errorf("ffmpeg not found: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}

	sorted := append([]commentary.Decision(nil), decisions...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Index < sorted[j].Index })

	fmt.Fprintln(out, "Audio Samples:")
	for _, decision := range sorted {
		lang := strings.TrimSpace(decision.Metadata.Language)
		if lang == "" {
			lang = "und"
		}
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "unknown"
		}
		filename := fmt.Sprintf("commentary_candidate_%d_%s_%s.opus", decision.Index, sanitizeToken(lang), sanitizeToken(reason))
		path := filepath.Join(cwd, filename)
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(out, "  - Warning: overwriting existing file %s\n", filename)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect output file %q: %w", path, err)
		}

		title := fmt.Sprintf("Commentary Candidate #%d (%s)", decision.Index, reason)
		args := []string{
			"-hide_banner", "-loglevel", "error",
			"-i", target,
			"-map", fmt.Sprintf("0:%d", decision.Index),
			"-t", "600",
			"-vn", "-sn", "-dn",
			"-c:a", "libopus",
			"-b:a", "128k",
			"-metadata:s:a:0", fmt.Sprintf("title=%s", title),
			"-metadata:s:a:0", fmt.Sprintf("language=%s", lang),
			"-y", path,
		}
		cmd := exec.CommandContext(ctx, ffmpegBinary, args...) //nolint:gosec
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			message := strings.TrimSpace(stderr.String())
			if message == "" {
				message = err.Error()
			}
			if strings.Contains(strings.ToLower(message), "unknown encoder 'libopus'") {
				return fmt.Errorf("ffmpeg does not support libopus encoding: %s", message)
			}
			return fmt.Errorf("ffmpeg opus encode failed for stream %d: %s", decision.Index, message)
		}
		fmt.Fprintf(out, "  - #%d -> %s\n", decision.Index, filename)
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
