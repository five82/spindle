package spindle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// StatusFetcher defines the interface for fetching Spindle status and logs.
// This interface is implemented by *Client and can be used for testing.
type StatusFetcher interface {
	FetchStatus(ctx context.Context) (*StatusResponse, error)
	FetchQueue(ctx context.Context) ([]QueueItem, error)
	FetchLogs(ctx context.Context, query LogQuery) (LogBatch, error)
}

// Ensure Client implements StatusFetcher at compile time.
var _ StatusFetcher = (*Client)(nil)

// Client talks to the Spindle HTTP API.
type Client struct {
	baseURL   *url.URL
	http      *http.Client
	userAgent string
	token     string
}

// ClientOption configures optional Client settings.
type ClientOption func(*Client)

// WithToken sets the bearer token for API authentication.
func WithToken(token string) ClientOption {
	return func(c *Client) {
		c.token = strings.TrimSpace(token)
	}
}

const (
	defaultUserAgent = "flyer/0.1"
	requestTimeout   = 5 * time.Second
)

// NewClient builds a client for a Spindle TCP API endpoint.
func NewClient(apiEndpoint string, opts ...ClientOption) (*Client, error) {
	base, err := parseBaseURL(apiEndpoint)
	if err != nil {
		return nil, err
	}
	c := &Client{
		baseURL: base,
		http: &http.Client{
			Timeout: requestTimeout,
		},
		userAgent: defaultUserAgent,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// FetchStatus retrieves daemon and workflow status information.
func (c *Client) FetchStatus(ctx context.Context) (*StatusResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("client is nil")
	}
	var payload StatusResponse
	if err := c.do(ctx, http.MethodGet, "/api/status", &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// FetchQueue retrieves the current queue snapshot.
func (c *Client) FetchQueue(ctx context.Context) ([]QueueItem, error) {
	if c == nil {
		return nil, fmt.Errorf("client is nil")
	}
	var payload QueueListResponse
	if err := c.do(ctx, http.MethodGet, "/api/queue", &payload); err != nil {
		return nil, err
	}
	return payload.Items, nil
}

// LogQuery configures /api/logs requests.
type LogQuery struct {
	Since      uint64
	Limit      int
	Tail       bool
	ItemID     int64
	Level      string
	Component  string
	Lane       string
	DaemonOnly bool // Only logs without item association (ItemID == 0)
	Request    string
}

// FetchLogs retrieves log events using the daemon's streaming API.
func (c *Client) FetchLogs(ctx context.Context, query LogQuery) (LogBatch, error) {
	if c == nil {
		return LogBatch{}, fmt.Errorf("client is nil")
	}
	values := url.Values{}
	if query.Since > 0 {
		values.Set("since", strconv.FormatUint(query.Since, 10))
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	if query.Tail {
		values.Set("tail", "1")
	}
	if query.ItemID > 0 {
		values.Set("item", strconv.FormatInt(query.ItemID, 10))
	}
	if level := strings.TrimSpace(query.Level); level != "" {
		values.Set("level", level)
	}
	if component := strings.TrimSpace(query.Component); component != "" {
		values.Set("component", component)
	}
	if lane := strings.TrimSpace(query.Lane); lane != "" {
		values.Set("lane", lane)
	}
	if query.DaemonOnly {
		values.Set("daemon_only", "1")
	}
	if req := strings.TrimSpace(query.Request); req != "" {
		values.Set("request", req)
	}
	rel := &url.URL{Path: "/api/logs", RawQuery: values.Encode()}
	var payload LogBatch
	if err := c.doURL(ctx, http.MethodGet, rel, &payload); err != nil {
		return LogBatch{}, err
	}
	return payload, nil
}

func (c *Client) do(ctx context.Context, method, path string, dest any) error {
	rel := &url.URL{Path: path}
	return c.doURL(ctx, method, rel, dest)
}

func (c *Client) doURL(ctx context.Context, method string, rel *url.URL, dest any) error {
	reqURL := c.baseURL.ResolveReference(rel)
	req, err := http.NewRequestWithContext(ctx, method, reqURL.String(), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return apiStatusError(rel, resp)
	}
	if dest == nil {
		return nil
	}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// apiErrorBodyLimit caps how much of an error response body is read when
// looking for a structured {"error":"..."} message.
const apiErrorBodyLimit = 4 * 1024

// apiStatusError builds the error for an HTTP status >= 400, preferring the
// server's structured {"error":"..."} message when the body provides one and
// falling back to a status-only error otherwise.
func apiStatusError(rel *url.URL, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, apiErrorBodyLimit))
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		if msg := strings.TrimSpace(payload.Error); msg != "" {
			return fmt.Errorf("api %s returned status %d: %s", rel.String(), resp.StatusCode, msg)
		}
	}
	return fmt.Errorf("api %s returned status %d", rel.String(), resp.StatusCode)
}

func parseBaseURL(apiEndpoint string) (*url.URL, error) {
	trimmed := strings.TrimSpace(apiEndpoint)
	if trimmed == "" {
		return nil, fmt.Errorf("API endpoint is empty; configure Spindle [api].bind or pass --api")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("parse API endpoint %q: %w", apiEndpoint, err)
	}
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}
