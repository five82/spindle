package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/queueops"
)

// Server is the HTTP API server.
type Server struct {
	store         *queue.Store
	token         string
	logger        *slog.Logger
	httpServer    *http.Server
	mux           *http.ServeMux
	discMonitor   *discmonitor.Monitor
	shutdownCh    chan struct{}
	statusInfo    StatusInfo
	logBuffer     *LogBuffer
	statusTracker *StatusTracker
	pipeline      []PipelineStageInfo
	scheduler     SchedulerSource
}

// New creates an HTTP API server. discMon, shutdownCh, logBuffer,
// statusTracker, pipeline, and scheduler may be nil.
func New(store *queue.Store, token string, discMon *discmonitor.Monitor, shutdownCh chan struct{}, logger *slog.Logger, statusInfo StatusInfo, logBuffer *LogBuffer, statusTracker *StatusTracker, pipeline []PipelineStageInfo, scheduler SchedulerSource) *Server {
	s := &Server{
		store:         store,
		token:         token,
		logger:        logger,
		mux:           http.NewServeMux(),
		discMonitor:   discMon,
		shutdownCh:    shutdownCh,
		statusInfo:    statusInfo,
		logBuffer:     logBuffer,
		statusTracker: statusTracker,
		pipeline:      pipeline,
		scheduler:     scheduler,
	}
	s.registerRoutes()
	s.httpServer = &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

// ListenUnix starts listening on a Unix socket.
func (s *Server) ListenUnix(path string) error {
	_ = os.Remove(path) // Clean up stale socket.
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen unix %s: %w", path, err)
	}
	go func() { _ = s.httpServer.Serve(ln) }()
	return nil
}

// ListenTCP starts listening on a TCP address.
func (s *Server) ListenTCP(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen tcp %s: %w", addr, err)
	}
	go func() { _ = s.httpServer.Serve(ln) }()
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// ServeHTTP implements http.Handler for testing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/queue", s.authMiddleware(s.handleQueueList))
	s.mux.HandleFunc("GET /api/queue/{id}", s.authMiddleware(s.handleQueueGet))
	s.mux.HandleFunc("POST /api/queue/retry", s.authMiddleware(s.handleQueueRetry))
	s.mux.HandleFunc("POST /api/queue/retry-episode", s.authMiddleware(s.handleQueueRetryEpisode))
	s.mux.HandleFunc("POST /api/queue/stop", s.authMiddleware(s.handleQueueStop))
	s.mux.HandleFunc("POST /api/queue/enqueue-cached", s.authMiddleware(s.handleQueueEnqueueCached))
	s.mux.HandleFunc("DELETE /api/queue/{id}", s.authMiddleware(s.handleQueueRemove))
	s.mux.HandleFunc("POST /api/queue/clear", s.authMiddleware(s.handleQueueClear))
	s.mux.HandleFunc("GET /api/logs", s.authMiddleware(s.handleLogs))
	s.mux.HandleFunc("GET /api/status", s.authMiddleware(s.handleStatus))
	s.mux.HandleFunc("GET /api/health", s.handleHealth) // no auth
	s.mux.HandleFunc("POST /api/daemon/stop", s.authMiddleware(s.handleDaemonStop))
	s.mux.HandleFunc("POST /api/disc/pause", s.authMiddleware(s.handleDiscPause))
	s.mux.HandleFunc("POST /api/disc/resume", s.authMiddleware(s.handleDiscResume))
	s.mux.HandleFunc("POST /api/disc/detect", s.authMiddleware(s.handleDiscDetect))
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "missing or invalid authorization header")
			return
		}
		if strings.TrimPrefix(auth, "Bearer ") != s.token {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleQueueList(w http.ResponseWriter, r *http.Request) {
	var stages []queue.Stage
	for _, v := range r.URL.Query()["stage"] {
		stages = append(stages, queue.Stage(v))
	}
	items, err := s.store.List(stages...)
	if err != nil {
		s.logger.Error("list queue items", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list queue items")
		return
	}
	responses := make([]ItemResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, toItemResponse(item, s.tasksFor(item.ID), false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": responses})
}

func (s *Server) handleQueueGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	item, err := s.store.GetByID(id)
	if err != nil {
		s.logger.Error("get queue item", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to get queue item")
		return
	}
	if item == nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": toItemResponse(item, s.tasksFor(item.ID), true)})
}

// tasksFor loads an item's task rows for response building; a load failure
// degrades to no tasks (the response's core fields still stand on their own).
func (s *Server) tasksFor(itemID int64) []*queue.Task {
	tasks, err := s.store.TasksForItem(itemID)
	if err != nil {
		s.logger.Warn("load tasks for response failed",
			"event_type", "queue_fetch_error",
			"error_hint", "task rows omitted from item response",
			"impact", "client sees item without task detail this poll",
			"item_id", itemID,
			"error", err,
		)
		return nil
	}
	return tasks
}

func (s *Server) handleQueueRetry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	count, err := s.store.RetryFailed(body.IDs...)
	if err != nil {
		s.logger.Error("retry failed items", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to retry items")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": count})
}

func (s *Server) handleQueueRetryEpisode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID         int64  `json:"id"`
		EpisodeKey string `json:"episode_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ID == 0 || body.EpisodeKey == "" {
		writeError(w, http.StatusBadRequest, "id and episode_key are required")
		return
	}
	result, err := queueops.RetryEpisode(s.store, body.ID, body.EpisodeKey)
	if err != nil {
		s.logger.Error("retry episode", "error", err, "id", body.ID, "episode_key", body.EpisodeKey)
		writeError(w, http.StatusInternalServerError, "failed to retry episode")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": string(result)})
}

func (s *Server) handleQueueStop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	count, err := s.store.StopItems(body.IDs...)
	if err != nil {
		s.logger.Error("stop items", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to stop items")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": count})
}

func (s *Server) handleQueueEnqueueCached(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DiscTitle      string `json:"disc_title"`
		Fingerprint    string `json:"fingerprint"`
		RipSpecData    string `json:"rip_spec_data"`
		MetadataJSON   string `json:"metadata_json"`
		AllowDuplicate bool   `json:"allow_duplicate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.DiscTitle = strings.TrimSpace(body.DiscTitle)
	body.Fingerprint = strings.TrimSpace(body.Fingerprint)
	if body.DiscTitle == "" || body.Fingerprint == "" || strings.TrimSpace(body.RipSpecData) == "" {
		writeError(w, http.StatusBadRequest, "disc_title, fingerprint, and rip_spec_data are required")
		return
	}
	if !body.AllowDuplicate {
		existing, err := s.store.FindByFingerprint(body.Fingerprint)
		if err != nil {
			s.logger.Error("check duplicate cached enqueue", "error", err, "fingerprint", body.Fingerprint)
			writeError(w, http.StatusInternalServerError, "failed to check duplicate fingerprint")
			return
		}
		if existing != nil {
			writeError(w, http.StatusConflict, fmt.Sprintf("fingerprint already queued (item %d, stage %s); use --allow-duplicate to override", existing.ID, existing.Stage))
			return
		}
	}
	item, err := s.store.NewCachedRip(body.DiscTitle, body.Fingerprint, body.RipSpecData, body.MetadataJSON)
	if err != nil {
		s.logger.Error("enqueue cached rip", "error", err, "fingerprint", body.Fingerprint)
		writeError(w, http.StatusInternalServerError, "failed to enqueue cached rip")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": toItemResponse(item, nil, false)})
}

func (s *Server) handleQueueRemove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.store.Remove(id); err != nil {
		s.logger.Error("remove queue item", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to remove item")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"removed": 1})
}

func (s *Server) handleQueueClear(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope string `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var count int64
	var err error
	switch body.Scope {
	case "all":
		count, err = s.store.Clear()
	case "completed":
		count, err = s.store.ClearCompleted()
	default:
		writeError(w, http.StatusBadRequest, "scope must be \"all\" or \"completed\"")
		return
	}
	if err != nil {
		s.logger.Error("clear queue", "error", err, "scope", body.Scope)
		writeError(w, http.StatusInternalServerError, "failed to clear queue")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"removed": count})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if s.logBuffer == nil {
		writeJSON(w, http.StatusOK, map[string]any{"events": []LogEntry{}, "next": 0})
		return
	}

	q := r.URL.Query()
	opts := LogQueryOpts{
		Component: q.Get("component"),
		Lane:      q.Get("lane"),
		Request:   q.Get("request"),
		Level:     q.Get("level"),
	}

	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.Limit = n
		}
	}
	if v := q.Get("since"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			opts.Since = n
		}
	}
	if v := q.Get("tail"); v == "1" || v == "true" {
		opts.Tail = true
	}
	if v := q.Get("item"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			opts.ItemID = n
		}
	}
	if v := q.Get("daemon_only"); v == "1" {
		opts.DaemonOnly = true
	}

	events, next := s.logBuffer.Query(opts)
	if events == nil {
		events = []LogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "next": next})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	stats, err := s.store.Stats()
	if err != nil {
		s.logger.Error("get queue stats", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get stats")
		return
	}
	queueStats := make(map[string]int, len(stats))
	for k, v := range stats {
		queueStats[string(k)] = v
	}

	wf := WorkflowStatus{
		Running:    true,
		QueueStats: queueStats,
	}
	deps := []DependencyResponse{}

	if s.statusTracker != nil {
		lastErr, trackerDeps := s.statusTracker.Snapshot()
		wf.LastError = lastErr
		if len(trackerDeps) > 0 {
			deps = trackerDeps
		}
	}

	resp := StatusAPIResponse{
		Running:      true,
		PID:          os.Getpid(),
		QueueDBPath:  s.statusInfo.QueueDBPath,
		LockFilePath: s.statusInfo.LockFilePath,
		Workflow:     wf,
		Dependencies: deps,
		Pipeline:     s.pipeline,
	}
	if s.scheduler != nil {
		resp.Scheduler = &SchedulerStatus{Resources: s.scheduler.SchedulerSnapshot()}
	}
	if s.discMonitor != nil {
		resp.Disc = &DiscStatus{Paused: s.discMonitor.IsPaused()}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDaemonStop(w http.ResponseWriter, _ *http.Request) {
	if s.shutdownCh == nil {
		writeError(w, http.StatusInternalServerError, "shutdown not supported")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
	// Close channel after writing response to signal daemon shutdown.
	select {
	case <-s.shutdownCh:
		// Already closed.
	default:
		close(s.shutdownCh)
	}
}

func (s *Server) handleDiscPause(w http.ResponseWriter, _ *http.Request) {
	if s.discMonitor == nil {
		writeError(w, http.StatusServiceUnavailable, "no optical drive configured")
		return
	}
	changed := s.discMonitor.PauseDisc()
	writeJSON(w, http.StatusOK, map[string]any{"paused": true, "changed": changed})
}

func (s *Server) handleDiscResume(w http.ResponseWriter, _ *http.Request) {
	if s.discMonitor == nil {
		writeError(w, http.StatusServiceUnavailable, "no optical drive configured")
		return
	}
	changed := s.discMonitor.ResumeDisc()
	writeJSON(w, http.StatusOK, map[string]any{"resumed": true, "changed": changed})
}

func (s *Server) handleDiscDetect(w http.ResponseWriter, r *http.Request) {
	if s.discMonitor == nil {
		writeError(w, http.StatusServiceUnavailable, "no optical drive configured")
		return
	}
	resp, err := s.discMonitor.DetectAsync(r.Context())
	if err != nil {
		s.logger.Error("disc detect failed", "error", err)
		writeError(w, http.StatusInternalServerError, "disc detect failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
