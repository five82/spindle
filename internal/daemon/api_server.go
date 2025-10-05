package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/api"
	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/queue"
)

type apiServer struct {
	bind     string
	logger   *slog.Logger
	daemon   *Daemon
	queueSvc *api.QueueService

	listener net.Listener
	server   *http.Server
}

func newAPIServer(cfg *config.Config, d *Daemon, logger *slog.Logger) (*apiServer, error) {
	if cfg == nil || d == nil {
		return nil, nil
	}
	bind := strings.TrimSpace(cfg.APIBind)
	if bind == "" {
		return nil, nil
	}

	svc := api.NewQueueService(d.store)
	mux := http.NewServeMux()
	srv := &apiServer{
		bind:     bind,
		logger:   logger,
		daemon:   d,
		queueSvc: svc,
	}

	mux.HandleFunc("/api/status", srv.handleStatus)
	mux.HandleFunc("/api/queue", srv.handleQueue)
	mux.HandleFunc("/api/queue/", srv.handleQueueItem)

	srv.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return srv, nil
}

func (s *apiServer) start(ctx context.Context) error {
	if s == nil {
		return nil
	}
	listener, err := net.Listen("tcp", s.bind)
	if err != nil {
		return fmt.Errorf("api listen: %w", err)
	}
	s.listener = listener

	go func() {
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log().Error("api server error", slog.String("error", err.Error()))
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx)
	}()

	s.log().Info("api server listening", slog.String("address", listener.Addr().String()))
	return nil
}

func (s *apiServer) stop() {
	if s == nil {
		return
	}
	if s.server != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx)
	}
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
}

func (s *apiServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status := s.daemon.Status(r.Context())
	deps := make([]api.DependencyStatus, len(status.Dependencies))
	for i, dep := range status.Dependencies {
		deps[i] = api.DependencyStatus{
			Name:        dep.Name,
			Command:     dep.Command,
			Description: dep.Description,
			Optional:    dep.Optional,
			Available:   dep.Available,
			Detail:      dep.Detail,
		}
	}
	payload := api.DaemonStatus{
		Running:      status.Running,
		PID:          status.PID,
		QueueDBPath:  status.QueueDBPath,
		LockFilePath: status.LockFilePath,
		Workflow:     api.FromStatusSummary(status.Workflow),
		Dependencies: deps,
	}
	s.writeJSON(w, http.StatusOK, payload)
}

func (s *apiServer) handleQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.queueSvc == nil {
		s.writeJSON(w, http.StatusOK, api.QueueListResponse{Items: nil})
		return
	}
	var statuses []queue.Status
	for _, value := range r.URL.Query()["status"] {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		statuses = append(statuses, queue.Status(trimmed))
	}

	items, err := s.queueSvc.List(r.Context(), statuses...)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, api.QueueListResponse{Items: items})
}

func (s *apiServer) handleQueueItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.queueSvc == nil {
		s.writeError(w, http.StatusNotFound, "queue item not found")
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/queue/")
	if idStr == "" || strings.Contains(idStr, "/") {
		s.writeError(w, http.StatusNotFound, "queue item not found")
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid queue item id")
		return
	}
	item, err := s.queueSvc.Describe(r.Context(), id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		s.writeError(w, http.StatusNotFound, "queue item not found")
		return
	}
	s.writeJSON(w, http.StatusOK, api.QueueItemResponse{Item: *item})
}

func (s *apiServer) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.log().Error("failed to encode response", slog.String("error", err.Error()))
	}
}

func (s *apiServer) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]string{"error": message})
}

func (s *apiServer) log() *slog.Logger {
	if s.logger != nil {
		return s.logger.With(logging.String("component", "api-server"))
	}
	return logging.NewNop()
}
