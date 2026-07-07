package httpapi_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

func testStore(t *testing.T) *queue.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := queue.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestHealthEndpoint(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %q", body["status"])
	}
}

func TestAuthRejectsMissingToken(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(httpapi.Params{Store: store, Token: "secret-token", Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})

	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthAcceptsValidToken(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(httpapi.Params{Store: store, Token: "secret-token", Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})

	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestQueueListReturnsWrappedEmptyArray(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})

	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Items) != 0 {
		t.Fatalf("expected empty items array, got %d items", len(body.Items))
	}
}

func TestQueueEnqueueCachedCreatesRippingItem(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})

	body := `{"disc_title":"Cached Disc","fingerprint":"fp1","rip_spec_data":"{\"version\":1}","metadata_json":"{\"title\":\"Cached Disc\"}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/queue/enqueue-cached", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Item struct {
			ID          int64  `json:"id"`
			Stage       string `json:"stage"`
			Fingerprint string `json:"discFingerprint"`
		} `json:"item"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Item.ID == 0 || resp.Item.Stage != string(queue.StageRipping) || resp.Item.Fingerprint != "fp1" {
		t.Fatalf("unexpected item response: %+v", resp.Item)
	}

	item, err := store.GetByID(resp.Item.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if item == nil || item.Stage != queue.StageRipping || item.RipSpecData == "" || item.MetadataJSON == "" {
		t.Fatalf("cached item not persisted correctly: %+v", item)
	}
}

func TestQueueEnqueueCachedRejectsDuplicate(t *testing.T) {
	store := testStore(t)
	if _, err := store.NewDisc("Existing", "fp1"); err != nil {
		t.Fatalf("new disc: %v", err)
	}
	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})

	body := `{"disc_title":"Cached Disc","fingerprint":"fp1","rip_spec_data":"{\"version\":1}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/queue/enqueue-cached", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestStatusReturnsStructuredResponse(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		Running  bool `json:"running"`
		PID      int  `json:"pid"`
		Workflow struct {
			Running    bool           `json:"running"`
			QueueStats map[string]int `json:"queueStats"`
		} `json:"workflow"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.Running {
		t.Fatal("expected running=true")
	}
	if body.PID <= 0 {
		t.Fatalf("expected positive PID, got %d", body.PID)
	}
	if !body.Workflow.Running {
		t.Fatal("expected workflow.running=true")
	}
}

// newProjectionEnvelope builds an envelope exercising every derived field:
// per-episode subtitle QC, per-episode audio analysis, and the content-ID
// summary.
func newProjectionEnvelope(t *testing.T) string {
	t.Helper()
	env := ripspec.Envelope{
		Version:     ripspec.CurrentVersion,
		Fingerprint: "fp1",
		Episodes: []ripspec.Episode{
			{Key: "s01_001", TitleID: 1, Season: 1, Episode: 1, EpisodeTitle: "Pilot"},
		},
		Attributes: ripspec.EnvelopeAttributes{
			AudioAnalysis: &ripspec.AudioAnalysisData{
				PrimaryDescription: "English 5.1",
				CommentaryTracks:   []ripspec.CommentaryTrackRef{{Index: 2}},
				PerEpisode: []ripspec.EpisodeAudioAnalysis{
					{
						EpisodeKey:       "s01_001",
						CommentaryTracks: []ripspec.CommentaryTrackRef{{Index: 2}},
						ExcludedTracks:   []ripspec.ExcludedTrackRef{{Index: 3}, {Index: 4}},
					},
				},
			},
			SubtitleGenerationResults: []ripspec.SubtitleGenRecord{
				{
					EpisodeKey:       "s01_001",
					Source:           "whisperx",
					Language:         "en",
					ValidationResult: "passed",
					ReviewIssues:     []string{"short segment near credits"},
				},
			},
			ContentID: &ripspec.ContentIDSummary{
				Method:          "transcript_match",
				MatchedEpisodes: 1,
				Completed:       true,
			},
		},
	}
	data, err := env.Encode()
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	return data
}

// TestQueueResponsesProjectTasksAndEnvelope covers the Phase A response
// contract: list responses carry tasks (with per-task progress and active
// asset keys feeding episode Active flags) and the completed episode
// projection but NO raw ripSpec; single-item GETs include ripSpec.
func TestQueueResponsesProjectTasksAndEnvelope(t *testing.T) {
	store := testStore(t)
	item, err := store.NewCachedRip("Show S01", "fp1", newProjectionEnvelope(t), "")
	if err != nil {
		t.Fatalf("new cached rip: %v", err)
	}
	specs := []queue.TaskSpec{
		{Type: queue.StageRipping},
		{Type: queue.StageEncoding, DependsOn: []queue.Stage{queue.StageRipping}},
	}
	if err := store.EnsureTasks(item, specs); err != nil {
		t.Fatalf("ensure tasks: %v", err)
	}
	tasks, err := store.TasksForItem(item.ID)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("tasks for item: %v (%d tasks)", err, len(tasks))
	}
	encoding := tasks[1]
	if err := store.StartTask(encoding); err != nil {
		t.Fatalf("start task: %v", err)
	}
	encoding.ProgressPercent = 42.5
	encoding.ProgressMessage = "Encoding s01_001"
	encoding.ActiveAssetKey = "s01_001"
	if err := store.UpdateTaskProgress(encoding); err != nil {
		t.Fatalf("update task progress: %v", err)
	}

	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})

	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var list struct {
		Items []httpapi.ItemResponse `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(list.Items))
	}
	got := list.Items[0]
	if got.RipSpec != nil {
		t.Fatal("list response must not include raw ripSpec")
	}
	if got.DisplayTitle == "" {
		t.Fatal("displayTitle missing")
	}
	if len(got.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(got.Tasks))
	}
	enc := got.Tasks[1]
	if enc.Type != string(queue.StageEncoding) || enc.State != string(queue.TaskRunning) {
		t.Fatalf("unexpected encoding task: %+v", enc)
	}
	if enc.Progress.Percent != 42.5 || enc.ActiveAssetKey != "s01_001" {
		t.Fatalf("task progress not projected: %+v", enc)
	}
	if len(enc.DependsOn) != 1 || enc.DependsOn[0] != string(queue.StageRipping) {
		t.Fatalf("dependsOn not resolved to type names: %+v", enc.DependsOn)
	}
	if len(got.Episodes) != 1 {
		t.Fatalf("expected 1 episode, got %d", len(got.Episodes))
	}
	ep := got.Episodes[0]
	if !ep.Active {
		t.Fatal("episode should be active via running task's asset key")
	}
	if ep.SubtitleValidation != "passed" || len(ep.SubtitleReviewIssues) != 1 {
		t.Fatalf("subtitle QC not projected: %+v", ep)
	}
	if ep.CommentaryTracks != 1 || ep.ExcludedTracks != 2 {
		t.Fatalf("per-episode audio analysis not projected: %+v", ep)
	}
	if got.ContentID == nil || got.ContentID.Method != "transcript_match" || !got.ContentID.Completed {
		t.Fatalf("contentId not projected: %+v", got.ContentID)
	}

	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/queue/%d", item.ID), nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var single struct {
		Item httpapi.ItemResponse `json:"item"`
	}
	if err := json.NewDecoder(w.Body).Decode(&single); err != nil {
		t.Fatalf("decode single: %v", err)
	}
	if single.Item.RipSpec == nil {
		t.Fatal("single-item GET must include raw ripSpec")
	}
}

// fakeScheduler implements httpapi.SchedulerSource for status tests.
type fakeScheduler struct{}

func (fakeScheduler) SchedulerSnapshot() map[string]httpapi.ResourceStatus {
	return map[string]httpapi.ResourceStatus{
		"drive": {Capacity: 1, Used: 1, Holders: []httpapi.ResourceHolder{{ItemID: 3, Task: "ripping"}}},
	}
}

// TestStatusIncludesPipelineAndScheduler covers the new /api/status surface.
func TestStatusIncludesPipelineAndScheduler(t *testing.T) {
	store := testStore(t)
	pipeline := []httpapi.PipelineStageInfo{
		{Stage: "identification", Claims: []string{"drive"}},
		{Stage: "ripping", DependsOn: []string{"identification"}, Claims: []string{"drive"}},
	}
	srv := httpapi.New(httpapi.Params{
		Store:     store,
		Logger:    slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Pipeline:  pipeline,
		Scheduler: fakeScheduler{},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body httpapi.StatusAPIResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Pipeline) != 2 || body.Pipeline[1].DependsOn[0] != "identification" {
		t.Fatalf("pipeline not exposed: %+v", body.Pipeline)
	}
	if body.Scheduler == nil {
		t.Fatal("scheduler missing")
	}
	drive := body.Scheduler.Resources["drive"]
	if drive.Used != 1 || len(drive.Holders) != 1 || drive.Holders[0].Task != "ripping" {
		t.Fatalf("drive occupancy not exposed: %+v", drive)
	}
}

func TestLogsItemQueryScopedToItemLifetime(t *testing.T) {
	store := testStore(t)

	// Simulate a queue clear followed by ID reuse: the log buffer holds
	// hydrated history from a previous item #1, then a fresh item #1 is
	// created and logs new lines.
	buffer := httpapi.NewLogBuffer(16)
	buffer.Append(httpapi.LogEntry{
		Time: "2026-05-24T14:12:20Z", ItemID: 1, Level: "ERROR", Msg: "old generation failure",
	})

	item, err := store.NewDisc("Breaking Bad", "fp-1")
	if err != nil {
		t.Fatalf("new disc: %v", err)
	}
	if item.ID != 1 {
		t.Fatalf("expected reused item ID 1, got %d", item.ID)
	}

	current := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	buffer.Append(httpapi.LogEntry{
		Time: current, ItemID: 1, Level: "WARN", Msg: "current generation warning",
	})

	srv := httpapi.New(httpapi.Params{
		Store:     store,
		Logger:    slog.New(slog.NewTextHandler(os.Stderr, nil)),
		LogBuffer: buffer,
	})

	fetch := func(target string) []httpapi.LogEntry {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var body struct {
			Events []httpapi.LogEntry `json:"events"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		return body.Events
	}

	// Item-scoped query returns only lines from this item's lifetime.
	events := fetch("/api/logs?item=1")
	if len(events) != 1 || events[0].Msg != "current generation warning" {
		t.Fatalf("item query returned %+v, want only the current-generation line", events)
	}

	// The unscoped daemon log keeps the full history.
	if events = fetch("/api/logs"); len(events) != 2 {
		t.Fatalf("unscoped query returned %d events, want 2", len(events))
	}
}
