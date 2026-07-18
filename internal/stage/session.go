package stage

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

// Session is the mutable work envelope for a single stage invocation. It keeps
// task progress, review state, and RipSpec persistence in one place so stage
// handlers can focus on domain work.
type Session struct {
	Ctx    context.Context
	Store  *queue.Store
	Item   *queue.Item
	Env    *ripspec.Envelope
	Logger *slog.Logger

	// Task is the running task this session reports progress against. Each
	// concurrent branch of an item has its own task row, so progress writes
	// never contend. A detached task (ID 0) keeps progress in memory only
	// (OneShot CLI execution, where no scheduler task exists).
	Task *queue.Task
}

// NewSession creates a stage session and parses the item's RipSpec envelope.
// task may be nil; a detached in-memory task is substituted.
func NewSession(ctx context.Context, store *queue.Store, item *queue.Item, task *queue.Task) (*Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if store == nil {
		return nil, fmt.Errorf("stage session: nil queue store")
	}
	if item == nil {
		return nil, fmt.Errorf("stage session: nil queue item")
	}
	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		return nil, fmt.Errorf("invalid rip spec: %w", err)
	}
	if task == nil {
		task = &queue.Task{ItemID: item.ID}
	}
	return &Session{
		Ctx:    ctx,
		Store:  store,
		Item:   item,
		Env:    &env,
		Logger: slog.Default(),
		Task:   task,
	}, nil
}

// SetEnvelope replaces the session's RipSpec envelope.
func (s *Session) SetEnvelope(env *ripspec.Envelope) {
	if env == nil {
		s.Env = &ripspec.Envelope{}
		return
	}
	s.Env = env
}

// itemLocks serializes envelope read-modify-write cycles so concurrent
// branches cannot lose each other's changes. Plain Save persists the whole
// envelope and is therefore last-writer-wins.
var (
	itemLocksMu sync.Mutex
	itemLocks   = make(map[int64]*sync.Mutex)
)

func itemLock(id int64) *sync.Mutex {
	itemLocksMu.Lock()
	defer itemLocksMu.Unlock()
	l, ok := itemLocks[id]
	if !ok {
		l = &sync.Mutex{}
		itemLocks[id] = l
	}
	return l
}

// MergeSave applies mutate to a FRESHLY loaded copy of the item's envelope
// under the per-item lock and persists it, then applies the same mutation
// to the session's in-memory envelope. Handlers whose stage can run
// concurrently with another stage of the same item MUST persist envelope
// changes only through merge operations (MergeSave, SaveAssetSuccess,
// MergeAddReviewReason) and never through plain Save.
func (s *Session) MergeSave(mutate func(*ripspec.Envelope) error) error {
	if s == nil || s.Store == nil || s.Item == nil {
		return fmt.Errorf("stage session: incomplete merge state")
	}
	lock := itemLock(s.Item.ID)
	lock.Lock()
	defer lock.Unlock()

	fresh, err := s.Store.GetByID(s.Item.ID)
	if err != nil {
		return fmt.Errorf("merge save load: %w", err)
	}
	if fresh == nil {
		return fmt.Errorf("merge save: item %d no longer exists", s.Item.ID)
	}
	env, err := ripspec.Parse(fresh.RipSpecData)
	if err != nil {
		return fmt.Errorf("merge save parse: %w", err)
	}
	if env.Version != ripspec.CurrentVersion {
		// The envelope has never been persisted (fresh DB state is empty).
		// Base the merge on a deep copy of the session's envelope, which is
		// the authoritative initial state in that case.
		encoded, encErr := s.Env.Encode()
		if encErr != nil {
			return encErr
		}
		env, err = ripspec.Parse(encoded)
		if err != nil {
			return fmt.Errorf("merge save seed: %w", err)
		}
	}
	if err := mutate(&env); err != nil {
		return err
	}
	data, err := env.Encode()
	if err != nil {
		return err
	}
	fresh.RipSpecData = data
	if err := s.Store.UpdateWorkState(fresh); err != nil {
		return err
	}

	// Apply the same mutation to the session's own envelope so its view
	// stays consistent WITHOUT adopting unrelated fresh state: handlers
	// accumulate in-memory changes they persist later, and replacing the
	// session envelope here would silently discard them. Mutations must
	// therefore be deterministic per copy (asset adds replace by key).
	if err := mutate(s.Env); err != nil {
		return err
	}
	return nil
}

// RefreshEnvelope reloads the item's envelope from the store under the
// per-item lock, adopting fresh state as the session's view. ONLY safe for
// handlers whose every envelope write goes through merge operations: any
// unsaved in-session envelope mutation is discarded. The encoder's
// streaming loop uses it to observe ripped assets the ripper persists
// concurrently.
func (s *Session) RefreshEnvelope() error {
	if s == nil || s.Store == nil || s.Item == nil {
		return fmt.Errorf("stage session: incomplete refresh state")
	}
	lock := itemLock(s.Item.ID)
	lock.Lock()
	defer lock.Unlock()

	fresh, err := s.Store.GetByID(s.Item.ID)
	if err != nil {
		return fmt.Errorf("refresh envelope load: %w", err)
	}
	if fresh == nil {
		return fmt.Errorf("refresh envelope: item %d no longer exists", s.Item.ID)
	}
	env, err := ripspec.Parse(fresh.RipSpecData)
	if err != nil {
		return fmt.Errorf("refresh envelope parse: %w", err)
	}
	s.Env = &env
	s.Item.RipSpecData = fresh.RipSpecData
	return nil
}

// MergeAddReviewReason appends a review reason against fresh item state
// under the per-item lock (the concurrent-stage-safe AddReviewReason).
func (s *Session) MergeAddReviewReason(reason string) error {
	if s == nil || s.Store == nil || s.Item == nil {
		return fmt.Errorf("stage session: incomplete merge state")
	}
	lock := itemLock(s.Item.ID)
	lock.Lock()
	defer lock.Unlock()

	fresh, err := s.Store.GetByID(s.Item.ID)
	if err != nil {
		return fmt.Errorf("merge review load: %w", err)
	}
	if fresh == nil {
		return fmt.Errorf("merge review: item %d no longer exists", s.Item.ID)
	}
	fresh.AppendReviewReason(reason)
	if err := s.Store.UpdateWorkState(fresh); err != nil {
		return err
	}
	s.Item.AppendReviewReason(reason)
	return nil
}

// Save persists the session's RipSpec envelope and queue-visible work state.
// Lifecycle fields such as stage, in_progress, failed_at_stage, and
// error_message are owned by stage execution and are not changed here.
func (s *Session) Save() error {
	if s == nil || s.Store == nil || s.Item == nil || s.Env == nil {
		return fmt.Errorf("stage session: incomplete save state")
	}
	ctx := s.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	data, err := s.Env.Encode()
	if err != nil {
		return err
	}
	s.Item.RipSpecData = data
	return s.Store.UpdateWorkState(s.Item)
}

// ProgressOption customizes a progress update.
type ProgressOption func(*progressUpdate)

type progressUpdate struct {
	activeEpisode *string
	bytesCopied   *int64
	totalBytes    *int64
	encodingJSON  *string
}

// WithActiveEpisode sets the task's active asset key during a progress update.
func WithActiveEpisode(key string) ProgressOption {
	return func(u *progressUpdate) { u.activeEpisode = &key }
}

// WithProgressBytes sets byte-copy progress during a progress update.
func WithProgressBytes(copied, total int64) ProgressOption {
	return func(u *progressUpdate) {
		u.bytesCopied = &copied
		u.totalBytes = &total
	}
}

// WithEncodingDetails sets the encoded telemetry JSON during a progress update.
func WithEncodingDetails(json string) ProgressOption {
	return func(u *progressUpdate) { u.encodingJSON = &json }
}

// Progress updates the running task's progress columns. Encoding telemetry
// rides along on the item (single writer: the encoding task). A detached
// task (ID 0) keeps progress in memory only.
func (s *Session) Progress(percent float64, message string, opts ...ProgressOption) error {
	if s == nil || s.Store == nil || s.Item == nil || s.Task == nil {
		return fmt.Errorf("stage session: incomplete progress state")
	}
	update := progressUpdate{}
	for _, opt := range opts {
		opt(&update)
	}

	s.Task.ProgressPercent = percent
	s.Task.ProgressMessage = message
	if update.activeEpisode != nil {
		s.Task.ActiveAssetKey = *update.activeEpisode
	}
	if update.bytesCopied != nil {
		s.Task.ProgressBytesCopied = *update.bytesCopied
	}
	if update.totalBytes != nil {
		s.Task.ProgressTotalBytes = *update.totalBytes
	}
	if update.encodingJSON != nil {
		s.Item.EncodingDetailsJSON = *update.encodingJSON
		if err := s.Store.UpdateEncodingDetails(s.Item); err != nil {
			return err
		}
	}
	if s.Task.ID == 0 {
		return nil
	}
	return s.Store.UpdateTaskProgress(s.Task)
}

// SetActiveEpisode persists a change to the task's active asset key without
// changing the current percent or message.
func (s *Session) SetActiveEpisode(key string) error {
	return s.Progress(s.Task.ProgressPercent, s.Task.ProgressMessage, WithActiveEpisode(key))
}

// ClearActiveEpisode clears the active asset key without changing current progress.
func (s *Session) ClearActiveEpisode() error { return s.SetActiveEpisode("") }

// AddReviewReason marks the item for review and appends a queue-level reason.
func (s *Session) AddReviewReason(reason string) {
	if s != nil && s.Item != nil {
		s.Item.AppendReviewReason(reason)
	}
}

// AddEpisodeReviewReason marks an episode for review. It returns false when no
// matching episode exists.
func (s *Session) AddEpisodeReviewReason(key, reason string) bool {
	if s == nil || s.Env == nil {
		return false
	}
	ep := s.Env.EpisodeByKey(key)
	if ep == nil {
		return false
	}
	ep.AppendReviewReason(reason)
	return true
}

// AddAsset appends or replaces an asset in the session envelope.
func (s *Session) AddAsset(kind string, asset ripspec.Asset) {
	if s != nil && s.Env != nil {
		s.Env.Assets.AddAsset(kind, asset)
	}
}
