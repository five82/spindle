package queueaccess

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/sockhttp"
)

// Access provides read-only queue access.
type Access interface {
	List(stages ...queue.Stage) ([]*queue.Item, error)
	GetByID(id int64) (*queue.Item, error)
	Stats() (map[queue.Stage]int, error)
}

// StoreAccess wraps a direct queue.Store.
type StoreAccess struct {
	Store *queue.Store
}

// List returns queue items, optionally filtered by stages.
func (a *StoreAccess) List(stages ...queue.Stage) ([]*queue.Item, error) {
	return a.Store.List(stages...)
}

// GetByID returns a single item by primary key.
func (a *StoreAccess) GetByID(id int64) (*queue.Item, error) { return a.Store.GetByID(id) }

// Stats returns item counts grouped by stage.
func (a *StoreAccess) Stats() (map[queue.Stage]int, error) { return a.Store.Stats() }

// HTTPAccess connects to the daemon HTTP API.
type HTTPAccess struct {
	socketPath string
	token      string
	client     *http.Client
}

// NewHTTPAccess creates an HTTP-based queue accessor.
func NewHTTPAccess(socketPath, token string) *HTTPAccess {
	return &HTTPAccess{
		socketPath: socketPath,
		token:      token,
		client:     sockhttp.NewUnixClient(socketPath, 10*time.Second),
	}
}

type queueListResponse struct {
	Items []queueItemResponse `json:"items"`
}

type queueGetResponse struct {
	Item queueItemResponse `json:"item"`
}

type statusResponse struct {
	Workflow struct {
		QueueStats map[string]int `json:"queueStats"`
	} `json:"workflow"`
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
	var resp statusResponse
	if err := a.getJSON("/api/status", &resp); err != nil {
		return nil, err
	}
	result := make(map[queue.Stage]int, len(resp.Workflow.QueueStats))
	for k, v := range resp.Workflow.QueueStats {
		result[queue.Stage(k)] = v
	}
	return result, nil
}

func (a *HTTPAccess) getJSON(path string, dest any) error {
	req, err := http.NewRequest(http.MethodGet, "http://localhost"+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	sockhttp.SetAuth(req, a.token)

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("http get %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %s: status %d: %s", path, resp.StatusCode, body)
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode response from %s: %w", path, err)
	}
	return nil
}

// OpenWithFallback tries HTTP first, falls back to direct store.
func OpenWithFallback(socketPath, token, dbPath string) (Access, error) {
	ha := NewHTTPAccess(socketPath, token)
	// Try a quick health check to see if the daemon is running.
	req, err := http.NewRequest(http.MethodGet, "http://localhost/api/health", nil)
	if err == nil {
		resp, err := ha.client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return ha, nil
			}
		}
	}

	// Fall back to direct store access.
	store, err := queue.OpenReadOnly(dbPath)
	if err != nil {
		return nil, fmt.Errorf("fallback to direct store: %w", err)
	}
	return &StoreAccess{Store: store}, nil
}
