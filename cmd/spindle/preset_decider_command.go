package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/encoding"
	"spindle/internal/services/llm"
)

const presetDeciderTestDescription = "Toy Story (type: movie) (year: 1995) (resolution: 1080p/HD) (source: blu-ray)"

func newPresetDeciderCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preset-decider",
		Short: "Preset decider tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newPresetDeciderTestCommand(ctx))
	return cmd
}

func newPresetDeciderTestCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Query the preset decider LLM and print its JSON response",
		Long: `Query the preset decider LLM using a fixed movie description in the same
format used by the encoder workflow. The output is the JSON payload returned by
the model.

This command does not touch the queue database or require the daemon.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}

			writeSection(cmd.ErrOrStderr(), "System Prompt", encoding.PresetClassificationPrompt)
			writeSection(cmd.ErrOrStderr(), "User Description", presetDeciderTestDescription)
			writeSection(cmd.ErrOrStderr(), "Response", "Printed to stdout as JSON.")

			client := llm.NewClient(llm.Config{
				APIKey:  cfg.PresetDecider.APIKey,
				BaseURL: cfg.PresetDecider.BaseURL,
				Model:   cfg.PresetDecider.Model,
				Referer: cfg.PresetDecider.Referer,
				Title:   cfg.PresetDecider.Title,
			})

			reqCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			classification, err := client.ClassifyPreset(reqCtx, encoding.PresetClassificationPrompt, presetDeciderTestDescription)
			if err != nil {
				return err
			}

			raw := strings.TrimSpace(classification.Raw)
			payload, err := normalizePresetDeciderJSON(raw)
			if err != nil {
				return fmt.Errorf("preset decider returned non-JSON payload: %w", err)
			}

			var buf bytes.Buffer
			if err := json.Indent(&buf, payload, "", "  "); err != nil {
				return fmt.Errorf("format JSON payload: %w", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), buf.String())
			return nil
		},
	}
}

func normalizePresetDeciderJSON(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(llm.StripCodeFenceBlock(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("empty payload")
	}

	candidate := trimmed
	if !strings.HasPrefix(candidate, "{") && !strings.HasPrefix(candidate, "[") {
		start := strings.Index(candidate, "{")
		end := strings.LastIndex(candidate, "}")
		if start >= 0 && end > start {
			candidate = strings.TrimSpace(candidate[start : end+1])
		}
	}

	var tmp any
	if err := json.Unmarshal([]byte(candidate), &tmp); err != nil {
		return nil, err
	}
	return []byte(candidate), nil
}

func writeSection(w interface {
	Write([]byte) (int, error)
}, title, body string) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Section"
	}
	_, _ = fmt.Fprintln(w, title)
	_, _ = fmt.Fprintln(w, strings.Repeat("-", len(title)))
	body = strings.TrimSpace(body)
	if body == "" {
		_, _ = fmt.Fprintln(w, "(empty)")
		_, _ = fmt.Fprintln(w, "")
		return
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		_, _ = fmt.Fprintf(w, "  %s\n", line)
	}
	_, _ = fmt.Fprintln(w, "")
}
