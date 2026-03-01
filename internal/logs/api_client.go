package logs

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

	"spindle/internal/api"
)

var ErrAPIUnavailable = errors.New("log API unavailable")

type StreamClient struct {
	base *url.URL
	http *http.Client
}

type StreamQuery struct {
	Since         uint64
	Limit         int
	Follow        bool
	Tail          bool
	Component     string
	Lane          string
	CorrelationID string
	ItemID        int64
	Level         string
	Alert         string
	DecisionType  string
	Search        string
}

func NewStreamClient(bind string) (*StreamClient, error) {
	bind = strings.TrimSpace(bind)
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

	return &StreamClient{
		base: base,
		// No timeout - follow mode blocks waiting for events until caller cancels.
		http: &http.Client{},
	}, nil
}

func (c *StreamClient) Fetch(ctx context.Context, q StreamQuery) (api.LogStreamResponse, error) {
	if c == nil {
		return api.LogStreamResponse{}, ErrAPIUnavailable
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
	if strings.TrimSpace(q.Component) != "" {
		values.Set("component", q.Component)
	}
	if strings.TrimSpace(q.Lane) != "" {
		values.Set("lane", q.Lane)
	}
	if strings.TrimSpace(q.CorrelationID) != "" {
		values.Set("correlation_id", q.CorrelationID)
	}
	if q.ItemID > 0 {
		values.Set("item", strconv.FormatInt(q.ItemID, 10))
	}
	if strings.TrimSpace(q.Level) != "" {
		values.Set("level", q.Level)
	}
	if strings.TrimSpace(q.Alert) != "" {
		values.Set("alert", q.Alert)
	}
	if strings.TrimSpace(q.DecisionType) != "" {
		values.Set("decision_type", q.DecisionType)
	}
	if strings.TrimSpace(q.Search) != "" {
		values.Set("search", q.Search)
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

func IsAPIUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}
	var opErr *net.OpError
	return errors.Is(err, ErrAPIUnavailable) || errors.As(err, &opErr)
}
