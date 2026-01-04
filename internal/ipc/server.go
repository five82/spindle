package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"sort"
	"sync"
	"time"

	"log/slog"

	"spindle/internal/api"
	"spindle/internal/daemon"
	"spindle/internal/logging"
	"spindle/internal/logs"
	"spindle/internal/queue"
)

// Server exposes daemon control via JSON-RPC over a Unix domain socket.
type Server struct {
	path      string
	daemon    *daemon.Daemon
	logger    *slog.Logger
	listener  net.Listener
	rpcServer *rpc.Server

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewServer configures the IPC server at the given socket path.
func NewServer(ctx context.Context, path string, d *daemon.Daemon, logger *slog.Logger) (*Server, error) {
	if d == nil {
		return nil, errors.New("ipc server requires daemon")
	}
	if logger == nil {
		logger = logging.NewNop()
	}

	if err := os.RemoveAll(path); err != nil {
		return nil, fmt.Errorf("remove existing socket: %w", err)
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on socket: %w", err)
	}

	rpcServer := rpc.NewServer()
	srv := &service{daemon: d, logger: logger, ctx: ctx}
	if err := rpcServer.RegisterName("Spindle", srv); err != nil {
		listener.Close()
		return nil, fmt.Errorf("register rpc service: %w", err)
	}

	serverCtx, cancel := context.WithCancel(ctx)
	return &Server{
		path:      path,
		daemon:    d,
		logger:    logger,
		listener:  listener,
		rpcServer: rpcServer,
		ctx:       serverCtx,
		cancel:    cancel,
	}, nil
}

// Serve starts accepting RPC connections until the context is canceled.
func (s *Server) Serve() {
	s.logger.Debug("IPC server listening", logging.String("socket", s.path))
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := s.listener.Accept()
			if err != nil {
				select {
				case <-s.ctx.Done():
					return
				default:
				}
				s.logger.Warn("accept failed",
					logging.Error(err),
					logging.String(logging.FieldEventType, "ipc_accept_failed"),
					logging.String("impact", "IPC clients may fail to connect"),
					logging.String(logging.FieldErrorHint, "Check socket permissions and restart the daemon if needed"))
				continue
			}
			s.wg.Add(1)
			go func(c net.Conn) {
				defer s.wg.Done()
				s.rpcServer.ServeCodec(jsonrpc.NewServerCodec(c))
			}(conn)
		}
	}()
}

// Close stops the server and removes the socket file.
func (s *Server) Close() {
	s.cancel()
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.wg.Wait()
	if err := os.RemoveAll(s.path); err != nil {
		s.logger.Warn("failed to remove socket",
			logging.String("socket", s.path),
			logging.Error(err),
			logging.String(logging.FieldEventType, "ipc_socket_cleanup_failed"),
			logging.String("impact", "stale IPC socket may block future starts"),
			logging.String(logging.FieldErrorHint, "Remove the socket file manually or rerun spindle stop"))
	}
}

type service struct {
	daemon *daemon.Daemon
	logger *slog.Logger
	ctx    context.Context
}

func (s *service) log() *slog.Logger {
	if s.logger == nil {
		return logging.NewNop()
	}
	return s.logger.With(logging.String("component", "ipc"))
}

func convertQueueItem(item *api.QueueItem) *QueueItem {
	if item == nil {
		return nil
	}
	qi := QueueItem(*item)
	return &qi
}

func (s *service) Start(_ StartRequest, resp *StartResponse) error {
	s.log().Debug("daemon start requested")
	if err := s.daemon.Start(s.ctx); err != nil {
		resp.Started = false
		resp.Message = err.Error()
		return nil
	}
	resp.Started = true
	resp.Message = "daemon started"
	s.log().Info("daemon started via IPC",
		logging.String(logging.FieldEventType, "daemon_start"))
	return nil
}

func (s *service) Stop(_ StopRequest, resp *StopResponse) error {
	s.log().Debug("daemon stop requested")
	s.daemon.Stop()
	resp.Stopped = true
	s.log().Info("daemon stopped via IPC",
		logging.String(logging.FieldEventType, "daemon_stop"))
	return nil
}

func (s *service) Status(_ StatusRequest, resp *StatusResponse) error {
	status := s.daemon.Status(s.ctx)
	resp.Running = status.Running
	resp.QueueDBPath = status.QueueDBPath
	resp.LockPath = status.LockFilePath
	resp.QueueStats = make(map[string]int, len(status.Workflow.QueueStats))
	resp.PID = status.PID
	for k, v := range status.Workflow.QueueStats {
		resp.QueueStats[string(k)] = v
	}
	resp.LastError = status.Workflow.LastError
	if status.Workflow.LastItem != nil {
		item := api.FromQueueItem(status.Workflow.LastItem)
		resp.LastItem = convertQueueItem(&item)
	}
	if len(status.Workflow.StageHealth) > 0 {
		resp.StageHealth = make([]StageHealth, 0, len(status.Workflow.StageHealth))
		names := make([]string, 0, len(status.Workflow.StageHealth))
		for name := range status.Workflow.StageHealth {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			health := status.Workflow.StageHealth[name]
			resp.StageHealth = append(resp.StageHealth, StageHealth{
				Name:   name,
				Ready:  health.Ready,
				Detail: health.Detail,
			})
		}
	}
	if len(status.Dependencies) > 0 {
		resp.Dependencies = make([]DependencyStatus, 0, len(status.Dependencies))
		for _, dep := range status.Dependencies {
			resp.Dependencies = append(resp.Dependencies, DependencyStatus{
				Name:        dep.Name,
				Command:     dep.Command,
				Description: dep.Description,
				Optional:    dep.Optional,
				Available:   dep.Available,
				Detail:      dep.Detail,
			})
		}
	}
	return nil
}

func (s *service) QueueList(req QueueListRequest, resp *QueueListResponse) error {
	statuses := make([]queue.Status, 0, len(req.Statuses))
	for _, status := range req.Statuses {
		parsed, ok := queue.ParseStatus(status)
		if !ok {
			continue
		}
		statuses = append(statuses, parsed)
	}
	items, err := s.daemon.ListQueue(s.ctx, statuses)
	if err != nil {
		return err
	}
	resp.Items = make([]QueueItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		dto := api.FromQueueItem(item)
		if qi := convertQueueItem(&dto); qi != nil {
			resp.Items = append(resp.Items, *qi)
		}
	}
	return nil
}

func (s *service) QueueDescribe(req QueueDescribeRequest, resp *QueueDescribeResponse) error {
	if req.ID <= 0 {
		return fmt.Errorf("invalid queue item id %d", req.ID)
	}
	item, err := s.daemon.GetQueueItem(s.ctx, req.ID)
	if err != nil {
		return err
	}
	if item == nil {
		return fmt.Errorf("queue item %d not found", req.ID)
	}
	dto := api.FromQueueItem(item)
	if qi := convertQueueItem(&dto); qi != nil {
		resp.Item = *qi
	}
	return nil
}

func (s *service) QueueClear(_ QueueClearRequest, resp *QueueClearResponse) error {
	s.log().Debug("queue clear requested")
	removed, err := s.daemon.ClearQueue(s.ctx)
	if err != nil {
		return err
	}
	resp.Removed = removed
	s.log().Info("queue cleared",
		logging.String(logging.FieldEventType, "queue_clear"),
		logging.Int64("removed_count", removed))
	return nil
}

func (s *service) QueueClearCompleted(_ QueueClearCompletedRequest, resp *QueueClearCompletedResponse) error {
	s.log().Debug("queue clear completed requested")
	removed, err := s.daemon.ClearCompleted(s.ctx)
	if err != nil {
		return err
	}
	resp.Removed = removed
	s.log().Info("queue completed items cleared",
		logging.String(logging.FieldEventType, "queue_clear_completed"),
		logging.Int64("removed_count", removed))
	return nil
}

func (s *service) QueueClearFailed(_ QueueClearFailedRequest, resp *QueueClearFailedResponse) error {
	s.log().Debug("queue clear failed requested")
	removed, err := s.daemon.ClearFailed(s.ctx)
	if err != nil {
		return err
	}
	resp.Removed = removed
	s.log().Info("queue failed items cleared",
		logging.String(logging.FieldEventType, "queue_clear_failed"),
		logging.Int64("removed_count", removed))
	return nil
}

func (s *service) QueueReset(_ QueueResetRequest, resp *QueueResetResponse) error {
	s.log().Debug("queue reset stuck requested")
	updated, err := s.daemon.ResetStuck(s.ctx)
	if err != nil {
		return err
	}
	resp.Updated = updated
	s.log().Info("queue stuck items reset",
		logging.String(logging.FieldEventType, "queue_reset_stuck"),
		logging.Int64("updated_count", updated))
	return nil
}

func (s *service) QueueRetry(req QueueRetryRequest, resp *QueueRetryResponse) error {
	s.log().Debug("queue retry requested", logging.Int("item_count", len(req.IDs)))
	updated, err := s.daemon.RetryFailed(s.ctx, req.IDs)
	if err != nil {
		return err
	}
	resp.Updated = updated
	s.log().Info("queue items retried",
		logging.String(logging.FieldEventType, "queue_retry"),
		logging.Int64("updated_count", updated))
	return nil
}

func (s *service) QueueStop(req QueueStopRequest, resp *QueueStopResponse) error {
	if len(req.IDs) == 0 {
		return errors.New("queue stop requires at least one id")
	}
	s.log().Debug("queue stop requested", logging.Int("item_count", len(req.IDs)))
	updated, err := s.daemon.StopQueueItems(s.ctx, req.IDs)
	if err != nil {
		return err
	}
	resp.Updated = updated
	s.log().Info("queue items stopped",
		logging.String(logging.FieldEventType, "queue_stop"),
		logging.Int64("updated_count", updated))
	return nil
}

func (s *service) LogTail(req LogTailRequest, resp *LogTailResponse) error {
	logPath := s.daemon.LogPath()
	if logPath == "" {
		resp.Offset = 0
		return nil
	}
	wait := time.Duration(req.WaitMillis) * time.Millisecond
	if wait <= 0 && req.Follow {
		wait = time.Second
	}
	options := logs.TailOptions{
		Offset: req.Offset,
		Limit:  req.Limit,
		Follow: req.Follow,
		Wait:   wait,
	}
	ctx := s.ctx
	if req.Follow && wait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(s.ctx, wait+500*time.Millisecond)
		defer cancel()
	}
	result, err := logs.Tail(ctx, logPath, options)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			resp.Offset = result.Offset
			return nil
		}
		return err
	}
	resp.Lines = result.Lines
	resp.Offset = result.Offset
	return nil
}

func (s *service) QueueHealth(_ QueueHealthRequest, resp *QueueHealthResponse) error {
	health, err := s.daemon.QueueHealth(s.ctx)
	if err != nil {
		return err
	}
	resp.Total = health.Total
	resp.Pending = health.Pending
	resp.Processing = health.Processing
	resp.Failed = health.Failed
	resp.Review = health.Review
	resp.Completed = health.Completed
	return nil
}

func (s *service) DatabaseHealth(_ DatabaseHealthRequest, resp *DatabaseHealthResponse) error {
	health, err := s.daemon.DatabaseHealth(s.ctx)
	if err != nil && health.Error == "" {
		return err
	}
	resp.DBPath = health.DBPath
	resp.DatabaseExists = health.DatabaseExists
	resp.DatabaseReadable = health.DatabaseReadable
	resp.SchemaVersion = health.SchemaVersion
	resp.TableExists = health.TableExists
	resp.ColumnsPresent = append(resp.ColumnsPresent, health.ColumnsPresent...)
	resp.MissingColumns = append(resp.MissingColumns, health.MissingColumns...)
	resp.IntegrityCheck = health.IntegrityCheck
	resp.TotalItems = health.TotalItems
	resp.Error = health.Error
	if err != nil {
		return err
	}
	return nil
}

func (s *service) TestNotification(_ TestNotificationRequest, resp *TestNotificationResponse) error {
	sent, message, err := s.daemon.TestNotification(s.ctx)
	resp.Sent = sent
	resp.Message = message
	return err
}
