package main

import (
	"context"
	"strings"

	"spindle/internal/api"
	"spindle/internal/ipc"
	"spindle/internal/queue"
)

type queueAPI interface {
	Stats(ctx context.Context) (map[string]int, error)
	List(ctx context.Context, statuses []string) ([]api.QueueItem, error)
	Describe(ctx context.Context, id int64) (*api.QueueItem, error)
	ClearAll(ctx context.Context) (int64, error)
	ClearCompleted(ctx context.Context) (int64, error)
	ClearFailed(ctx context.Context) (int64, error)
	Remove(ctx context.Context, ids []int64) (int64, error)
	ResetStuck(ctx context.Context) (int64, error)
	RetryAll(ctx context.Context) (int64, error)
	Retry(ctx context.Context, ids []int64) (int64, error)
	RetryEpisode(ctx context.Context, itemID int64, episodeKey string) (api.RetryItemResult, error)
	Stop(ctx context.Context, ids []int64) (int64, error)
	Health(ctx context.Context) (queue.HealthSummary, error)
	ActiveFingerprints(ctx context.Context) (map[string]struct{}, error)
}

// queueStoreAPI extends queueAPI with operations that require direct store access.
type queueStoreAPI interface {
	queueAPI
}

// --- IPC adapter ---

type queueIPCAdapter struct {
	client *ipc.Client
}

func (a *queueIPCAdapter) Stats(_ context.Context) (map[string]int, error) {
	resp, err := a.client.Status()
	if err != nil {
		return nil, err
	}
	return resp.QueueStats, nil
}

func (a *queueIPCAdapter) List(_ context.Context, statuses []string) ([]api.QueueItem, error) {
	resp, err := a.client.QueueList(statuses)
	if err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (a *queueIPCAdapter) Describe(_ context.Context, id int64) (*api.QueueItem, error) {
	resp, err := a.client.QueueDescribe(id)
	if err != nil {
		return nil, err
	}
	if resp == nil || !resp.Found {
		return nil, nil
	}
	return &resp.Item, nil
}

func (a *queueIPCAdapter) ClearAll(_ context.Context) (int64, error) {
	resp, err := a.client.QueueClear()
	if err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

func (a *queueIPCAdapter) ClearCompleted(_ context.Context) (int64, error) {
	resp, err := a.client.QueueClearCompleted()
	if err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

func (a *queueIPCAdapter) ClearFailed(_ context.Context) (int64, error) {
	resp, err := a.client.QueueClearFailed()
	if err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

func (a *queueIPCAdapter) Remove(_ context.Context, ids []int64) (int64, error) {
	resp, err := a.client.QueueRemove(ids)
	if err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

func (a *queueIPCAdapter) ResetStuck(_ context.Context) (int64, error) {
	resp, err := a.client.QueueReset()
	if err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

func (a *queueIPCAdapter) RetryAll(_ context.Context) (int64, error) {
	resp, err := a.client.QueueRetry(nil)
	if err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

func (a *queueIPCAdapter) Retry(_ context.Context, ids []int64) (int64, error) {
	resp, err := a.client.QueueRetry(ids)
	if err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

func (a *queueIPCAdapter) RetryEpisode(_ context.Context, itemID int64, episodeKey string) (api.RetryItemResult, error) {
	resp, err := a.client.QueueRetryEpisode(itemID, episodeKey)
	if err != nil {
		return api.RetryItemResult{}, err
	}
	if resp == nil {
		return api.RetryItemResult{}, nil
	}
	return resp.Result, nil
}

func (a *queueIPCAdapter) Stop(_ context.Context, ids []int64) (int64, error) {
	resp, err := a.client.QueueStop(ids)
	if err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

func (a *queueIPCAdapter) Health(_ context.Context) (queue.HealthSummary, error) {
	resp, err := a.client.QueueHealth()
	if err != nil {
		return queue.HealthSummary{}, err
	}
	return queue.HealthSummary(*resp), nil
}

func (a *queueIPCAdapter) ActiveFingerprints(ctx context.Context) (map[string]struct{}, error) {
	items, err := a.List(ctx, nil)
	if err != nil {
		return nil, err
	}
	fingerprints := make(map[string]struct{}, len(items))
	for _, item := range items {
		fp := strings.ToUpper(strings.TrimSpace(item.DiscFingerprint))
		if fp != "" {
			fingerprints[fp] = struct{}{}
		}
	}
	return fingerprints, nil
}

// --- Store adapter ---

type queueStoreAdapter struct {
	store   *queue.Store
	service *api.QueueService
}

func (a *queueStoreAdapter) Stats(ctx context.Context) (map[string]int, error) {
	return a.service.Stats(ctx)
}

func (a *queueStoreAdapter) List(ctx context.Context, statuses []string) ([]api.QueueItem, error) {
	var filters []queue.Status
	for _, s := range statuses {
		if parsed, ok := queue.ParseStatus(s); ok {
			filters = append(filters, parsed)
		}
	}
	return a.service.List(ctx, filters...)
}

func (a *queueStoreAdapter) Describe(ctx context.Context, id int64) (*api.QueueItem, error) {
	return a.service.Describe(ctx, id)
}

func (a *queueStoreAdapter) ClearAll(ctx context.Context) (int64, error) {
	return a.store.Clear(ctx)
}

func (a *queueStoreAdapter) ClearCompleted(ctx context.Context) (int64, error) {
	return a.store.ClearCompleted(ctx)
}

func (a *queueStoreAdapter) ClearFailed(ctx context.Context) (int64, error) {
	return a.store.ClearFailed(ctx)
}

func (a *queueStoreAdapter) Remove(ctx context.Context, ids []int64) (int64, error) {
	var count int64
	for _, id := range ids {
		removed, err := a.store.Remove(ctx, id)
		if err != nil {
			return count, err
		}
		if removed {
			count++
		}
	}
	return count, nil
}

func (a *queueStoreAdapter) ResetStuck(ctx context.Context) (int64, error) {
	return a.store.ResetStuckProcessing(ctx)
}

func (a *queueStoreAdapter) RetryAll(ctx context.Context) (int64, error) {
	return a.store.RetryFailed(ctx)
}

func (a *queueStoreAdapter) Retry(ctx context.Context, ids []int64) (int64, error) {
	return a.store.RetryFailed(ctx, ids...)
}

func (a *queueStoreAdapter) RetryEpisode(ctx context.Context, itemID int64, episodeKey string) (api.RetryItemResult, error) {
	return api.RetryFailedEpisodeByID(ctx, a.store, itemID, episodeKey)
}

func (a *queueStoreAdapter) Stop(ctx context.Context, ids []int64) (int64, error) {
	return a.store.StopItems(ctx, ids...)
}

func (a *queueStoreAdapter) Health(ctx context.Context) (queue.HealthSummary, error) {
	return a.store.Health(ctx)
}

func (a *queueStoreAdapter) ActiveFingerprints(ctx context.Context) (map[string]struct{}, error) {
	items, err := a.store.List(ctx)
	if err != nil {
		return nil, err
	}
	fingerprints := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		fp := strings.ToUpper(strings.TrimSpace(item.DiscFingerprint))
		if fp != "" {
			fingerprints[fp] = struct{}{}
		}
	}
	return fingerprints, nil
}
