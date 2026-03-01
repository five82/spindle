package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/api"
	"spindle/internal/config"
	"spindle/internal/ipc"
	"spindle/internal/logs"
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
			filters := logFilters{
				Component: componentFilter,
				Lane:      laneFilter,
				RequestID: requestFilter,
				ItemID:    itemFilter,
				Level:     levelFilter,
				Alert:     alertFilter,
				Decision:  decisionFilter,
				Search:    searchFilter,
			}
			if err := streamLogsFromAPI(cmd, cfg, lines, follow, filters); err == nil {
				return nil
			} else if !errors.Is(err, errLogAPIUnavailable) {
				return err
			}
			if !filters.empty() {
				return fmt.Errorf("log filters require API access: %w", errLogAPIUnavailable)
			}

			initialLimit := lines
			if initialLimit < 0 {
				initialLimit = 0
			}
			initialOffset := int64(-1)
			if initialLimit == 0 {
				initialOffset = 0
			}

			return ctx.withClient(func(client *ipc.Client) error {
				ctx := cmd.Context()
				offset := initialOffset
				limit := initialLimit
				waitMillis := 1000
				printed := false

				for {
					req := ipc.LogTailRequest{
						Offset:     offset,
						Limit:      limit,
						Follow:     follow,
						WaitMillis: waitMillis,
					}
					resp, err := client.LogTail(req)
					if err != nil {
						return fmt.Errorf("tail logs: %w", err)
					}
					if resp == nil {
						return errors.New("log tail response missing")
					}
					for _, line := range resp.Lines {
						fmt.Fprintln(cmd.OutOrStdout(), line)
						printed = true
					}
					offset = resp.Offset
					limit = 0
					if !follow {
						if !printed {
							fmt.Fprintln(cmd.OutOrStdout(), "No log entries available")
						}
						return nil
					}
					select {
					case <-ctx.Done():
						return nil
					default:
					}
				}
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

var errLogAPIUnavailable = errors.New("log API unavailable")

func streamLogsFromAPI(cmd *cobra.Command, cfg *config.Config, lines int, follow bool, filters logFilters) error {
	client, err := newLogAPIClient(cfg)
	if err != nil {
		return err
	}
	if client == nil {
		return errLogAPIUnavailable
	}

	ctx := cmd.Context()
	query := logs.StreamQuery{
		Limit:         lines,
		Tail:          true,
		Component:     filters.Component,
		Lane:          filters.Lane,
		CorrelationID: filters.RequestID,
		ItemID:        filters.ItemID,
		Level:         filters.Level,
		Alert:         filters.Alert,
		DecisionType:  filters.Decision,
		Search:        filters.Search,
	}
	if query.Limit <= 0 {
		query.Limit = 200
	}

	printed := false
	for {
		resp, err := client.Fetch(ctx, query)
		if err != nil {
			if logs.IsAPIUnavailable(err) {
				return errLogAPIUnavailable
			}
			return err
		}
		for _, evt := range resp.Events {
			fmt.Fprintln(cmd.OutOrStdout(), formatAPILogEvent(evt))
			printed = true
		}
		if !follow {
			if !printed {
				fmt.Fprintln(cmd.OutOrStdout(), "No log entries available")
			}
			return nil
		}
		query.Since = resp.Next
		query.Limit = 200
		query.Tail = false
		query.Follow = true
	}
}

type logFilters struct {
	Component string
	Lane      string
	RequestID string
	ItemID    int64
	Level     string
	Alert     string
	Decision  string
	Search    string
}

func (f logFilters) empty() bool {
	return strings.TrimSpace(f.Component) == "" &&
		strings.TrimSpace(f.Lane) == "" &&
		strings.TrimSpace(f.RequestID) == "" &&
		strings.TrimSpace(f.Level) == "" &&
		strings.TrimSpace(f.Alert) == "" &&
		strings.TrimSpace(f.Decision) == "" &&
		strings.TrimSpace(f.Search) == "" &&
		f.ItemID == 0
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
	subject := composeSubject(evt.Lane, evt.ItemID, evt.Stage)
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

func composeSubject(lane string, itemID int64, stage string) string {
	lane = strings.TrimSpace(lane)
	stage = strings.TrimSpace(stage)
	parts := make([]string, 0, 3)
	if lane != "" {
		var formatted string
		if len(lane) > 1 {
			formatted = strings.ToUpper(lane[:1]) + strings.ToLower(lane[1:])
		} else {
			formatted = strings.ToUpper(lane)
		}
		parts = append(parts, formatted)
	}
	switch {
	case itemID > 0 && stage != "":
		parts = append(parts, fmt.Sprintf("Item #%d (%s)", itemID, stage))
	case itemID > 0:
		parts = append(parts, fmt.Sprintf("Item #%d", itemID))
	case stage != "":
		parts = append(parts, stage)
	}
	return strings.Join(parts, " · ")
}
