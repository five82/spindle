package main

import (
	"context"
	"fmt"
	"strings"

	"spindle/internal/api"
	"spindle/internal/ipc"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
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
	Stop(ctx context.Context, ids []int64) (int64, error)
	Health(ctx context.Context) (queue.HealthSummary, error)
}

// queueStoreAPI extends queueAPI with operations that require direct store access.
type queueStoreAPI interface {
	queueAPI
	RetryEpisode(ctx context.Context, itemID int64, episodeKey string) (queueRetryItemResult, error)
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
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil, nil
		}
		return nil, err
	}
	if resp == nil {
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

func (a *queueStoreAdapter) Stop(ctx context.Context, ids []int64) (int64, error) {
	return a.store.StopItems(ctx, ids...)
}

func (a *queueStoreAdapter) Health(ctx context.Context) (queue.HealthSummary, error) {
	return a.store.Health(ctx)
}

// RetryEpisode clears a specific episode's failed status and resets the item
// to the appropriate stage so it can be re-processed.
func (a *queueStoreAdapter) RetryEpisode(ctx context.Context, itemID int64, episodeKey string) (queueRetryItemResult, error) {
	item, err := a.store.GetByID(ctx, itemID)
	if err != nil {
		return queueRetryItemResult{}, err
	}
	if item == nil {
		return queueRetryItemResult{ID: itemID, Outcome: queueRetryOutcomeNotFound}, nil
	}

	if item.Status != queue.StatusFailed {
		return queueRetryItemResult{ID: itemID, Outcome: queueRetryOutcomeNotFailed}, nil
	}

	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		return queueRetryItemResult{}, fmt.Errorf("parse rip spec: %w", err)
	}

	episodeKey = strings.ToLower(strings.TrimSpace(episodeKey))
	if episodeKey == "" {
		return queueRetryItemResult{ID: itemID, Outcome: queueRetryOutcomeEpisodeNotFound}, nil
	}

	targetStatus := determineRetryStatus(&env, episodeKey)
	if targetStatus == "" {
		return queueRetryItemResult{ID: itemID, Outcome: queueRetryOutcomeEpisodeNotFound}, nil
	}

	env.Assets.ClearFailedAsset(ripspec.AssetKindEncoded, episodeKey)
	env.Assets.ClearFailedAsset(ripspec.AssetKindSubtitled, episodeKey)
	env.Assets.ClearFailedAsset(ripspec.AssetKindFinal, episodeKey)

	encoded, err := env.Encode()
	if err != nil {
		return queueRetryItemResult{}, fmt.Errorf("encode rip spec: %w", err)
	}

	item.RipSpecData = encoded
	item.Status = targetStatus
	item.ErrorMessage = ""
	item.NeedsReview = false
	item.ReviewReason = ""

	if err := a.store.Update(ctx, item); err != nil {
		return queueRetryItemResult{}, fmt.Errorf("update item: %w", err)
	}

	return queueRetryItemResult{
		ID:        itemID,
		Outcome:   queueRetryOutcomeUpdated,
		NewStatus: string(targetStatus),
	}, nil
}

// determineRetryStatus figures out which status to reset the item to based on
// which asset failed for the given episode.
func determineRetryStatus(env *ripspec.Envelope, episodeKey string) queue.Status {
	episode := env.EpisodeByKey(episodeKey)
	if episode == nil {
		return ""
	}

	if asset, ok := env.Assets.FindAsset(ripspec.AssetKindFinal, episodeKey); ok && asset.IsFailed() {
		return queue.StatusEncoded
	}
	if asset, ok := env.Assets.FindAsset(ripspec.AssetKindSubtitled, episodeKey); ok && asset.IsFailed() {
		return queue.StatusEncoded
	}
	if asset, ok := env.Assets.FindAsset(ripspec.AssetKindEncoded, episodeKey); ok && asset.IsFailed() {
		if len(env.Episodes) > 0 {
			return queue.StatusEpisodeIdentified
		}
		return queue.StatusRipped
	}

	return queue.StatusRipped
}
