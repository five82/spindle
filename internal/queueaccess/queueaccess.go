package queueaccess

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/queueops"
	"github.com/five82/spindle/internal/sockhttp"
)

// ErrDaemonUnavailable is returned when the daemon HTTP API cannot be reached.
var ErrDaemonUnavailable = errors.New("daemon is not running; run spindle start")

// Access provides daemon-backed queue access.
type Access interface {
	List(stages ...queue.Stage) ([]*queue.Item, error)
	GetByID(id int64) (*queue.Item, error)
	Stats() (map[queue.Stage]int, error)
	Status() (*Status, error)
	Retry(ids ...int64) (int, error)
	RetryEpisode(id int64, episodeKey string) (queueops.RetryResult, error)
	Stop(ids ...int64) (int, error)
	EnqueueCached(req EnqueueCachedRequest) (*queue.Item, error)
	Clear(scope string) (int64, error)
	Remove(id int64) (int64, error)
}

// HTTPAccess connects to the daemon HTTP API.
type HTTPAccess struct {
	token  string
	client *http.Client
}

// NewHTTPAccess creates an HTTP-based queue accessor.
func NewHTTPAccess(socketPath, token string) *HTTPAccess {
	return &HTTPAccess{
		token:  token,
		client: sockhttp.NewUnixClient(socketPath, 10*time.Second),
	}
}

// OpenHTTP verifies that the daemon API is reachable and returns an HTTP accessor.
func OpenHTTP(socketPath, token string) (*HTTPAccess, error) {
	a := NewHTTPAccess(socketPath, token)
	req, err := http.NewRequest(http.MethodGet, "http://localhost/api/health", nil)
	if err != nil {
		return nil, fmt.Errorf("create health request: %w", err)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, ErrDaemonUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrDaemonUnavailable
	}
	return a, nil
}

type queueListResponse struct {
	Items []queueItemResponse `json:"items"`
}

type queueGetResponse struct {
	Item queueItemResponse `json:"item"`
}

type queueRetryResponse struct {
	Updated int `json:"updated"`
}

type queueClearResponse struct {
	Removed int64 `json:"removed"`
}

type queueRetryEpisodeResponse struct {
	Result queueops.RetryResult `json:"result"`
}

type queueEnqueueCachedResponse struct {
	Item queueItemResponse `json:"item"`
}

// EnqueueCachedRequest describes a cached rip queue request.
type EnqueueCachedRequest struct {
	DiscTitle      string `json:"disc_title"`
	Fingerprint    string `json:"fingerprint"`
	RipSpecData    string `json:"rip_spec_data"`
	MetadataJSON   string `json:"metadata_json"`
	AllowDuplicate bool   `json:"allow_duplicate"`
}

// Status is the daemon status response used by CLI rendering.
type Status struct {
	Running      bool
	PID          int
	QueueDBPath  string
	LockFilePath string
	Workflow     WorkflowStatus
	Dependencies []DependencyStatus
}

// WorkflowStatus is the daemon workflow status used by CLI rendering.
type WorkflowStatus struct {
	Running    bool
	QueueStats map[queue.Stage]int
	LastError  string
	LastItem   *queue.Item
}

// DependencyStatus reports an external dependency health check.
type DependencyStatus struct {
	Name        string
	Command     string
	Description string
	Optional    bool
	Available   bool
	Detail      string
}

type statusAPIResponse struct {
	Running      bool                       `json:"running"`
	PID          int                        `json:"pid"`
	QueueDBPath  string                     `json:"queueDbPath"`
	LockFilePath string                     `json:"lockFilePath"`
	Workflow     workflowStatusAPIResponse  `json:"workflow"`
	Dependencies []dependencyStatusResponse `json:"dependencies"`
}

type workflowStatusAPIResponse struct {
	Running    bool               `json:"running"`
	QueueStats map[string]int     `json:"queueStats"`
	LastError  string             `json:"lastError"`
	LastItem   *queueItemResponse `json:"lastItem"`
}

type dependencyStatusResponse struct {
	Name        string `json:"name"`
	Command     string `json:"command"`
	Description string `json:"description"`
	Optional    bool   `json:"optional"`
	Available   bool   `json:"available"`
	Detail      string `json:"detail"`
}

type queueItemResponse struct {
	ID               int64            `json:"id"`
	DiscTitle        string           `json:"discTitle"`
	Stage            string           `json:"stage"`
	InProgress       bool             `json:"inProgress"`
	FailedAtStage    string           `json:"failedAtStage"`
	ErrorMessage     string           `json:"errorMessage"`
	CreatedAt        string           `json:"createdAt"`
	UpdatedAt        string           `json:"updatedAt"`
	DiscFingerprint  string           `json:"discFingerprint"`
	NeedsReview      bool             `json:"needsReview"`
	ReviewReason     string           `json:"reviewReason"`
	Metadata         json.RawMessage  `json:"metadata"`
	RipSpec          json.RawMessage  `json:"ripSpec"`
	ActiveEpisodeKey string           `json:"activeEpisodeKey"`
	Progress         progressResponse `json:"progress"`
	Encoding         json.RawMessage  `json:"encoding"`
}

type progressResponse struct {
	Stage       string  `json:"stage"`
	Percent     float64 `json:"percent"`
	Message     string  `json:"message"`
	BytesCopied int64   `json:"bytesCopied"`
	TotalBytes  int64   `json:"totalBytes"`
}

func (r queueItemResponse) toQueueItem() *queue.Item {
	item := &queue.Item{
		ID:                  r.ID,
		DiscTitle:           r.DiscTitle,
		Stage:               queue.Stage(r.Stage),
		FailedAtStage:       r.FailedAtStage,
		ErrorMessage:        r.ErrorMessage,
		CreatedAt:           r.CreatedAt,
		UpdatedAt:           r.UpdatedAt,
		DiscFingerprint:     r.DiscFingerprint,
		ReviewReason:        r.ReviewReason,
		ActiveEpisodeKey:    r.ActiveEpisodeKey,
		ProgressStage:       r.Progress.Stage,
		ProgressPercent:     r.Progress.Percent,
		ProgressMessage:     r.Progress.Message,
		ProgressBytesCopied: r.Progress.BytesCopied,
		ProgressTotalBytes:  r.Progress.TotalBytes,
	}
	if r.InProgress {
		item.InProgress = 1
	}
	if r.NeedsReview {
		item.NeedsReview = 1
	}
	item.MetadataJSON = rawJSONToString(r.Metadata)
	item.RipSpecData = rawJSONToString(r.RipSpec)
	item.EncodingDetailsJSON = rawJSONToString(r.Encoding)
	return item
}

func rawJSONToString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return string(raw)
}

// List returns queue items via HTTP, optionally filtered by stages.
func (a *HTTPAccess) List(stages ...queue.Stage) ([]*queue.Item, error) {
	path := "/api/queue"
	if len(stages) > 0 {
		params := url.Values{}
		for _, s := range stages {
			params.Add("stage", string(s))
		}
		path += "?" + params.Encode()
	}
	var resp queueListResponse
	if err := a.getJSON(path, &resp); err != nil {
		return nil, err
	}
	items := make([]*queue.Item, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, item.toQueueItem())
	}
	return items, nil
}

// GetByID returns a single item by ID via HTTP.
func (a *HTTPAccess) GetByID(id int64) (*queue.Item, error) {
	var resp queueGetResponse
	if err := a.getJSON(fmt.Sprintf("/api/queue/%d", id), &resp); err != nil {
		return nil, err
	}
	return resp.Item.toQueueItem(), nil
}

// Stats returns item counts grouped by stage via HTTP.
func (a *HTTPAccess) Stats() (map[queue.Stage]int, error) {
	status, err := a.Status()
	if err != nil {
		return nil, err
	}
	return status.Workflow.QueueStats, nil
}

// Status returns daemon status via HTTP.
func (a *HTTPAccess) Status() (*Status, error) {
	var resp statusAPIResponse
	if err := a.getJSON("/api/status", &resp); err != nil {
		return nil, err
	}

	stats := make(map[queue.Stage]int, len(resp.Workflow.QueueStats))
	for k, v := range resp.Workflow.QueueStats {
		stats[queue.Stage(k)] = v
	}

	var lastItem *queue.Item
	if resp.Workflow.LastItem != nil {
		lastItem = resp.Workflow.LastItem.toQueueItem()
	}

	deps := make([]DependencyStatus, 0, len(resp.Dependencies))
	for _, dep := range resp.Dependencies {
		deps = append(deps, DependencyStatus(dep))
	}

	return &Status{
		Running:      resp.Running,
		PID:          resp.PID,
		QueueDBPath:  resp.QueueDBPath,
		LockFilePath: resp.LockFilePath,
		Workflow: WorkflowStatus{
			Running:    resp.Workflow.Running,
			QueueStats: stats,
			LastError:  resp.Workflow.LastError,
			LastItem:   lastItem,
		},
		Dependencies: deps,
	}, nil
}

// Retry retries failed queue items via HTTP. No IDs means retry all failed items.
func (a *HTTPAccess) Retry(ids ...int64) (int, error) {
	var resp queueRetryResponse
	if err := a.postJSON("/api/queue/retry", map[string]any{"ids": ids}, &resp); err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

// RetryEpisode retries a single failed episode via HTTP.
func (a *HTTPAccess) RetryEpisode(id int64, episodeKey string) (queueops.RetryResult, error) {
	var resp queueRetryEpisodeResponse
	body := map[string]any{"id": id, "episode_key": episodeKey}
	if err := a.postJSON("/api/queue/retry-episode", body, &resp); err != nil {
		return "", err
	}
	return resp.Result, nil
}

// Stop marks queue items stopped via HTTP.
func (a *HTTPAccess) Stop(ids ...int64) (int, error) {
	var resp queueRetryResponse
	if err := a.postJSON("/api/queue/stop", map[string]any{"ids": ids}, &resp); err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

// EnqueueCached queues a cached rip for processing via HTTP.
func (a *HTTPAccess) EnqueueCached(req EnqueueCachedRequest) (*queue.Item, error) {
	var resp queueEnqueueCachedResponse
	if err := a.postJSON("/api/queue/enqueue-cached", req, &resp); err != nil {
		return nil, err
	}
	return resp.Item.toQueueItem(), nil
}

// Clear clears queue items by scope via HTTP.
func (a *HTTPAccess) Clear(scope string) (int64, error) {
	var resp queueClearResponse
	if err := a.postJSON("/api/queue/clear", map[string]any{"scope": scope}, &resp); err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

// Remove removes a queue item by ID via HTTP.
func (a *HTTPAccess) Remove(id int64) (int64, error) {
	var resp queueClearResponse
	if err := a.deleteJSON(fmt.Sprintf("/api/queue/%d", id), &resp); err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

func (a *HTTPAccess) getJSON(path string, dest any) error {
	req, err := http.NewRequest(http.MethodGet, "http://localhost"+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	return a.doJSON(req, dest)
}

func (a *HTTPAccess) postJSON(path string, body any, dest any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, "http://localhost"+path, &buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return a.doJSON(req, dest)
}

func (a *HTTPAccess) deleteJSON(path string, dest any) error {
	req, err := http.NewRequest(http.MethodDelete, "http://localhost"+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	return a.doJSON(req, dest)
}

func (a *HTTPAccess) doJSON(req *http.Request, dest any) error {
	sockhttp.SetAuth(req, a.token)

	resp, err := a.client.Do(req)
	if err != nil {
		return ErrDaemonUnavailable
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response from %s: %w", req.URL.Path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("http %s: status %d: %s", req.URL.Path, resp.StatusCode, errResp.Error)
		}
		return fmt.Errorf("http %s: status %d: %s", req.URL.Path, resp.StatusCode, string(body))
	}

	if dest == nil {
		return nil
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode response from %s: %w", req.URL.Path, err)
	}
	return nil
}
