package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/api"
	"spindle/internal/config"
	"spindle/internal/ipc"
	"spindle/internal/logging"
	"spindle/internal/logs"
	"spindle/internal/logstream"
)

func newShowCommand(ctx *commandContext) *cobra.Command {
	var follow bool
	var lines int
	var componentFilter string
	var laneFilter string
	var requestFilter string
	var itemFilter int64
	var levelFilter string
	var alertFilter string
	var decisionFilter string
	var searchFilter string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Display daemon logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return err
			}
			streamClient, err := newLogAPIClient(cfg)
			if err != nil {
				return err
			}
			opts := logstream.Options{
				Lines:  lines,
				Follow: follow,
				Filters: logstream.Filters{
					Component: componentFilter,
					Lane:      laneFilter,
					RequestID: requestFilter,
					ItemID:    itemFilter,
					Level:     levelFilter,
					Alert:     alertFilter,
					Decision:  decisionFilter,
					Search:    searchFilter,
				},
			}

			printed, err := logstream.Stream(
				cmd.Context(),
				streamClient,
				nil,
				opts,
				func(evt api.LogEvent) {
					fmt.Fprintln(cmd.OutOrStdout(), formatAPILogEvent(evt))
				},
				func(line string) {
					fmt.Fprintln(cmd.OutOrStdout(), line)
				},
			)
			if err == nil {
				if !follow && !printed {
					fmt.Fprintln(cmd.OutOrStdout(), "No log entries available")
				}
				return nil
			}
			if errors.Is(err, logstream.ErrFiltersRequireAPI) {
				return fmt.Errorf("log filters require API access: %w", logs.ErrAPIUnavailable)
			}
			if !logs.IsAPIUnavailable(err) {
				return err
			}

			return ctx.withClient(func(client *ipc.Client) error {
				printed, streamErr := logstream.Stream(
					cmd.Context(),
					streamClient,
					client,
					opts,
					func(evt api.LogEvent) {
						fmt.Fprintln(cmd.OutOrStdout(), formatAPILogEvent(evt))
					},
					func(line string) {
						fmt.Fprintln(cmd.OutOrStdout(), line)
					},
				)
				if streamErr != nil {
					return streamErr
				}
				if !follow && !printed {
					fmt.Fprintln(cmd.OutOrStdout(), "No log entries available")
				}
				return nil
			})
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")
	cmd.Flags().IntVarP(&lines, "lines", "n", 10, "Number of lines to show (0 for all)")
	cmd.Flags().StringVar(&componentFilter, "component", "", "Filter logs by component label")
	cmd.Flags().StringVar(&laneFilter, "lane", "", "Filter logs by workflow lane (foreground/background)")
	cmd.Flags().StringVar(&requestFilter, "request", "", "Filter logs by request/correlation ID")
	cmd.Flags().Int64VarP(&itemFilter, "item", "i", 0, "Filter logs by queue item ID")
	cmd.Flags().StringVar(&levelFilter, "level", "", "Minimum log level (debug, info, warn, error)")
	cmd.Flags().StringVar(&alertFilter, "alert", "", "Filter logs by alert flag")
	cmd.Flags().StringVar(&decisionFilter, "decision-type", "", "Filter logs by decision type")
	cmd.Flags().StringVar(&searchFilter, "search", "", "Search logs by substring")
	return cmd
}

func newLogAPIClient(cfg *config.Config) (*logs.StreamClient, error) {
	if cfg == nil {
		return nil, nil
	}
	return logs.NewStreamClient(cfg.Paths.APIBind)
}

func formatAPILogEvent(evt api.LogEvent) string {
	ts := evt.Timestamp.Format("2006-01-02 15:04:05")
	level := strings.ToUpper(strings.TrimSpace(evt.Level))
	if level == "" {
		level = "INFO"
	}
	parts := []string{ts, level}
	if component := strings.TrimSpace(evt.Component); component != "" {
		parts = append(parts, fmt.Sprintf("[%s]", component))
	}
	itemID := ""
	if evt.ItemID > 0 {
		itemID = strconv.FormatInt(evt.ItemID, 10)
	}
	subject := logging.FormatSubject(evt.Lane, itemID, evt.Stage)
	line := strings.Join(parts, " ")
	if subject != "" {
		line += " " + subject
	}
	message := strings.TrimSpace(evt.Message)
	if message != "" {
		line += " – " + message
	}
	if rid := strings.TrimSpace(evt.CorrelationID); rid != "" {
		line += fmt.Sprintf(" {req:%s}", rid)
	}
	if len(evt.Details) == 0 {
		return line
	}
	builder := strings.Builder{}
	builder.WriteString(line)
	for _, detail := range evt.Details {
		if strings.TrimSpace(detail.Label) == "" || strings.TrimSpace(detail.Value) == "" {
			continue
		}
		builder.WriteString("\n    - ")
		builder.WriteString(detail.Label)
		builder.WriteString(": ")
		builder.WriteString(detail.Value)
	}
	return builder.String()
}
