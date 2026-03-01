package logstream

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"spindle/internal/api"
	"spindle/internal/ipc"
	"spindle/internal/logs"
)

var ErrFiltersRequireAPI = errors.New("log filters require API access")

// TailClient captures the IPC log tail contract used for fallback streaming.
type TailClient interface {
	LogTail(req ipc.LogTailRequest) (*ipc.LogTailResponse, error)
}

// Filters contains optional predicates supported by API log streaming.
type Filters struct {
	Component string
	Lane      string
	RequestID string
	ItemID    int64
	Level     string
	Alert     string
	Decision  string
	Search    string
}

func (f Filters) empty() bool {
	return strings.TrimSpace(f.Component) == "" &&
		strings.TrimSpace(f.Lane) == "" &&
		strings.TrimSpace(f.RequestID) == "" &&
		strings.TrimSpace(f.Level) == "" &&
		strings.TrimSpace(f.Alert) == "" &&
		strings.TrimSpace(f.Decision) == "" &&
		strings.TrimSpace(f.Search) == "" &&
		f.ItemID == 0
}

// Options controls stream behavior.
type Options struct {
	Lines   int
	Follow  bool
	Filters Filters
}

// Stream emits log lines from API when available, falling back to IPC tailing.
// It returns true when at least one line/event was emitted.
func Stream(
	ctx context.Context,
	apiClient *logs.StreamClient,
	legacy TailClient,
	opts Options,
	onEvent func(api.LogEvent),
	onLine func(string),
) (bool, error) {
	printed, err := streamAPI(ctx, apiClient, opts, onEvent)
	if err == nil {
		return printed, nil
	}
	if !logs.IsAPIUnavailable(err) {
		return printed, err
	}
	if !opts.Filters.empty() {
		return false, fmt.Errorf("%w: %w", ErrFiltersRequireAPI, logs.ErrAPIUnavailable)
	}
	if legacy == nil {
		return false, logs.ErrAPIUnavailable
	}
	return streamLegacy(ctx, legacy, opts, onLine)
}

func streamAPI(
	ctx context.Context,
	client *logs.StreamClient,
	opts Options,
	onEvent func(api.LogEvent),
) (bool, error) {
	query := logs.StreamQuery{
		Limit:         opts.Lines,
		Tail:          true,
		Component:     opts.Filters.Component,
		Lane:          opts.Filters.Lane,
		CorrelationID: opts.Filters.RequestID,
		ItemID:        opts.Filters.ItemID,
		Level:         opts.Filters.Level,
		Alert:         opts.Filters.Alert,
		DecisionType:  opts.Filters.Decision,
		Search:        opts.Filters.Search,
	}
	if query.Limit <= 0 {
		query.Limit = 200
	}

	printed := false
	for {
		resp, err := client.Fetch(ctx, query)
		if err != nil {
			return printed, err
		}
		for _, evt := range resp.Events {
			if onEvent != nil {
				onEvent(evt)
			}
			printed = true
		}
		if !opts.Follow {
			return printed, nil
		}
		query.Since = resp.Next
		query.Limit = 200
		query.Tail = false
		query.Follow = true
	}
}

func streamLegacy(ctx context.Context, client TailClient, opts Options, onLine func(string)) (bool, error) {
	initialLimit := opts.Lines
	if initialLimit < 0 {
		initialLimit = 0
	}
	initialOffset := int64(-1)
	if initialLimit == 0 {
		initialOffset = 0
	}

	offset := initialOffset
	limit := initialLimit
	waitMillis := 1000
	printed := false
	for {
		req := ipc.LogTailRequest{
			Offset:     offset,
			Limit:      limit,
			Follow:     opts.Follow,
			WaitMillis: waitMillis,
		}
		resp, err := client.LogTail(req)
		if err != nil {
			return printed, fmt.Errorf("tail logs: %w", err)
		}
		if resp == nil {
			return printed, errors.New("log tail response missing")
		}
		for _, line := range resp.Lines {
			if onLine != nil {
				onLine(line)
			}
			printed = true
		}
		offset = resp.Offset
		limit = 0
		if !opts.Follow {
			return printed, nil
		}
		select {
		case <-ctx.Done():
			return printed, nil
		default:
		}
	}
}
