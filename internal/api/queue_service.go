package api

import (
	"context"

	"spindle/internal/queue"
)

// QueueReader abstracts queue persistence interactions needed for API queries.
type QueueReader interface {
	List(ctx context.Context, statuses ...queue.Status) ([]*queue.Item, error)
	Stats(ctx context.Context) (map[queue.Status]int, error)
	GetByID(ctx context.Context, id int64) (*queue.Item, error)
}

// QueueService exposes read-only queue operations returning API DTOs.
type QueueService struct {
	store QueueReader
}

// NewQueueService constructs a QueueService around the provided reader.
func NewQueueService(store QueueReader) *QueueService {
	if store == nil {
		return nil
	}
	return &QueueService{store: store}
}

// List returns queue items filtered by status.
func (s *QueueService) List(ctx context.Context, statuses ...queue.Status) ([]QueueItem, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	items, err := s.store.List(ctx, statuses...)
	if err != nil {
		return nil, err
	}
	return FromQueueItems(items), nil
}

// Stats returns queue summary counts keyed by status string.
func (s *QueueService) Stats(ctx context.Context) (map[string]int, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	stats, err := s.store.Stats(ctx)
	if err != nil {
		return nil, err
	}
	return MergeQueueStats(stats), nil
}

// Describe fetches a single queue item.
func (s *QueueService) Describe(ctx context.Context, id int64) (*QueueItem, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	item, err := s.store.GetByID(ctx, id)
	if err != nil || item == nil {
		return nil, err
	}
	dto := FromQueueItem(item)
	return &dto, nil
}
