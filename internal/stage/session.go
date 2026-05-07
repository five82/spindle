package stage

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

// Session is the mutable work envelope for a single stage invocation. It keeps
// queue item progress, review state, and RipSpec persistence in one place so
// stage handlers can focus on domain work.
type Session struct {
	Ctx    context.Context
	Store  *queue.Store
	Item   *queue.Item
	Env    *ripspec.Envelope
	Logger *slog.Logger
}

// NewSession creates a stage session and parses the item's RipSpec envelope.
func NewSession(ctx context.Context, store *queue.Store, item *queue.Item) (*Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if store == nil {
		return nil, fmt.Errorf("stage session: nil queue store")
	}
	if item == nil {
		return nil, fmt.Errorf("stage session: nil queue item")
	}
	env, err := ParseRipSpec(item.RipSpecData)
	if err != nil {
		return nil, err
	}
	return &Session{
		Ctx:    ctx,
		Store:  store,
		Item:   item,
		Env:    &env,
		Logger: slog.Default(),
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

// Save persists the session's RipSpec envelope and queue-visible work state.
// Lifecycle fields such as stage, in_progress, failed_at_stage, and
// error_message are owned by workflow/stageexec and are not changed here.
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

// WithActiveEpisode sets active_episode_key during a progress update.
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

// Progress updates the item progress fields and persists them with UpdateProgress.
func (s *Session) Progress(percent float64, message string, opts ...ProgressOption) error {
	if s == nil || s.Store == nil || s.Item == nil {
		return fmt.Errorf("stage session: incomplete progress state")
	}
	update := progressUpdate{}
	for _, opt := range opts {
		opt(&update)
	}

	s.Item.ProgressPercent = percent
	s.Item.ProgressMessage = message
	if update.activeEpisode != nil {
		s.Item.ActiveEpisodeKey = *update.activeEpisode
	}
	if update.bytesCopied != nil {
		s.Item.ProgressBytesCopied = *update.bytesCopied
	}
	if update.totalBytes != nil {
		s.Item.ProgressTotalBytes = *update.totalBytes
	}
	if update.encodingJSON != nil {
		s.Item.EncodingDetailsJSON = *update.encodingJSON
	}
	return s.Store.UpdateProgress(s.Item)
}

// SetActiveEpisode persists a change to active_episode_key without changing
// the current percent or message.
func (s *Session) SetActiveEpisode(key string) error {
	return s.Progress(s.Item.ProgressPercent, s.Item.ProgressMessage, WithActiveEpisode(key))
}

// ClearActiveEpisode clears active_episode_key without changing current progress.
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
