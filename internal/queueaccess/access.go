package queueaccess

import (
	"context"
	"strings"

	"spindle/internal/api"
	"spindle/internal/ipc"
	"spindle/internal/queue"
)

// Access provides queue operations regardless of IPC or direct store backing.
type Access interface {
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
	ActiveFingerprints(ctx context.Context) (map[string]struct{}, error)
}

// NewIPCAccess returns an Access backed by daemon IPC.
func NewIPCAccess(client *ipc.Client) Access {
	return &ipcAccess{client: client}
}

// NewStoreAccess returns an Access backed by direct DB access.
func NewStoreAccess(store *queue.Store) Access {
	return &storeAccess{store: store, service: api.NewQueueService(store)}
}

type ipcAccess struct {
	client *ipc.Client
}

func (a *ipcAccess) Stats(_ context.Context) (map[string]int, error) {
	resp, err := a.client.Status()
	if err != nil {
		return nil, err
	}
	return resp.QueueStats, nil
}

func (a *ipcAccess) List(_ context.Context, statuses []string) ([]api.QueueItem, error) {
	resp, err := a.client.QueueList(statuses)
	if err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (a *ipcAccess) Describe(_ context.Context, id int64) (*api.QueueItem, error) {
	resp, err := a.client.QueueDescribe(id)
	if err != nil {
		return nil, err
	}
	if resp == nil || !resp.Found {
		return nil, nil
	}
	return &resp.Item, nil
}

func (a *ipcAccess) ClearAll(_ context.Context) (int64, error) {
	resp, err := a.client.QueueClear()
	if err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

func (a *ipcAccess) ClearCompleted(_ context.Context) (int64, error) {
	resp, err := a.client.QueueClearCompleted()
	if err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

func (a *ipcAccess) ClearFailed(_ context.Context) (int64, error) {
	resp, err := a.client.QueueClearFailed()
	if err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

func (a *ipcAccess) Remove(_ context.Context, ids []int64) (int64, error) {
	resp, err := a.client.QueueRemove(ids)
	if err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

func (a *ipcAccess) ResetStuck(_ context.Context) (int64, error) {
	resp, err := a.client.QueueReset()
	if err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

func (a *ipcAccess) RetryAll(_ context.Context) (int64, error) {
	resp, err := a.client.QueueRetry(nil)
	if err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

func (a *ipcAccess) Retry(_ context.Context, ids []int64) (int64, error) {
	resp, err := a.client.QueueRetry(ids)
	if err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

func (a *ipcAccess) RetryEpisode(_ context.Context, itemID int64, episodeKey string) (api.RetryItemResult, error) {
	resp, err := a.client.QueueRetryEpisode(itemID, episodeKey)
	if err != nil {
		return api.RetryItemResult{}, err
	}
	if resp == nil {
		return api.RetryItemResult{}, nil
	}
	return resp.Result, nil
}

func (a *ipcAccess) Stop(_ context.Context, ids []int64) (int64, error) {
	resp, err := a.client.QueueStop(ids)
	if err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

func (a *ipcAccess) ActiveFingerprints(ctx context.Context) (map[string]struct{}, error) {
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

type storeAccess struct {
	store   *queue.Store
	service *api.QueueService
}

func (a *storeAccess) Stats(ctx context.Context) (map[string]int, error) {
	return a.service.Stats(ctx)
}

func (a *storeAccess) List(ctx context.Context, statuses []string) ([]api.QueueItem, error) {
	var filters []queue.Status
	for _, s := range statuses {
		if parsed, ok := queue.ParseStatus(s); ok {
			filters = append(filters, parsed)
		}
	}
	return a.service.List(ctx, filters...)
}

func (a *storeAccess) Describe(ctx context.Context, id int64) (*api.QueueItem, error) {
	return a.service.Describe(ctx, id)
}

func (a *storeAccess) ClearAll(ctx context.Context) (int64, error) {
	return a.store.Clear(ctx)
}

func (a *storeAccess) ClearCompleted(ctx context.Context) (int64, error) {
	return a.store.ClearCompleted(ctx)
}

func (a *storeAccess) ClearFailed(ctx context.Context) (int64, error) {
	return a.store.ClearFailed(ctx)
}

func (a *storeAccess) Remove(ctx context.Context, ids []int64) (int64, error) {
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

func (a *storeAccess) ResetStuck(ctx context.Context) (int64, error) {
	return a.store.ResetStuckProcessing(ctx)
}

func (a *storeAccess) RetryAll(ctx context.Context) (int64, error) {
	return a.store.RetryFailed(ctx)
}

func (a *storeAccess) Retry(ctx context.Context, ids []int64) (int64, error) {
	return a.store.RetryFailed(ctx, ids...)
}

func (a *storeAccess) RetryEpisode(ctx context.Context, itemID int64, episodeKey string) (api.RetryItemResult, error) {
	return api.RetryFailedEpisode(ctx, a.store, itemID, episodeKey)
}

func (a *storeAccess) Stop(ctx context.Context, ids []int64) (int64, error) {
	return a.store.StopItems(ctx, ids...)
}

func (a *storeAccess) ActiveFingerprints(ctx context.Context) (map[string]struct{}, error) {
	return a.store.ActiveFingerprints(ctx)
}
