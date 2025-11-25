package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/api"
	"spindle/internal/config"
	"spindle/internal/ipc"
)

func newShowCommand(ctx *commandContext) *cobra.Command {
	var follow bool
	var lines int

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Display daemon logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return err
			}
			if err := streamLogsFromAPI(cmd, cfg, lines, follow); err == nil {
				return nil
			} else if !errors.Is(err, errLogAPIUnavailable) {
				return err
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
	return cmd
}

var errLogAPIUnavailable = errors.New("log API unavailable")

func streamLogsFromAPI(cmd *cobra.Command, cfg *config.Config, lines int, follow bool) error {
	client, err := newLogAPIClient(cfg)
	if err != nil {
		return err
	}
	if client == nil {
		return errLogAPIUnavailable
	}

	ctx := cmd.Context()
	query := logAPIQuery{
		Limit: lines,
		Tail:  true,
	}
	if query.Limit <= 0 {
		query.Limit = 200
	}

	printed := false
	for {
		resp, err := client.Fetch(ctx, query)
		if err != nil {
			if isLogAPIUnavailable(err) {
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

type logAPIClient struct {
	base *url.URL
	http *http.Client
}

type logAPIQuery struct {
	Since  uint64
	Limit  int
	Follow bool
	Tail   bool
}

func newLogAPIClient(cfg *config.Config) (*logAPIClient, error) {
	if cfg == nil {
		return nil, nil
	}
	bind := strings.TrimSpace(cfg.APIBind)
	if bind == "" {
		return nil, nil
	}
	if !strings.Contains(bind, "://") {
		bind = "http://" + bind
	}
	base, err := url.Parse(bind)
	if err != nil {
		return nil, err
	}
	base.Path = ""
	base.RawQuery = ""
	base.Fragment = ""
	return &logAPIClient{
		base: base,
		http: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (c *logAPIClient) Fetch(ctx context.Context, q logAPIQuery) (api.LogStreamResponse, error) {
	if c == nil {
		return api.LogStreamResponse{}, errLogAPIUnavailable
	}
	values := url.Values{}
	if q.Since > 0 {
		values.Set("since", strconv.FormatUint(q.Since, 10))
	}
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Follow {
		values.Set("follow", "1")
	}
	if q.Tail {
		values.Set("tail", "1")
	}

	endpoint := c.base.ResolveReference(&url.URL{Path: "/api/logs", RawQuery: values.Encode()})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return api.LogStreamResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return api.LogStreamResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return api.LogStreamResponse{}, fmt.Errorf("api logs returned status %d", resp.StatusCode)
	}
	var payload api.LogStreamResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return api.LogStreamResponse{}, err
	}
	return payload, nil
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
	subject := composeSubject(evt.ItemID, evt.Stage)
	line := strings.Join(parts, " ")
	if subject != "" {
		line += " " + subject
	}
	message := strings.TrimSpace(evt.Message)
	if message != "" {
		line += " – " + message
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

func composeSubject(itemID int64, stage string) string {
	stage = strings.TrimSpace(stage)
	switch {
	case itemID > 0 && stage != "":
		return fmt.Sprintf("Item #%d (%s)", itemID, stage)
	case itemID > 0:
		return fmt.Sprintf("Item #%d", itemID)
	default:
		return stage
	}
}

func isLogAPIUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}
