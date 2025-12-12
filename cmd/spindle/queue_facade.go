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
	List(ctx context.Context, statuses []string) ([]queueItemView, error)
	Describe(ctx context.Context, id int64) (*queueItemDetailsView, error)
	ClearAll(ctx context.Context) (int64, error)
	ClearCompleted(ctx context.Context) (int64, error)
	ClearFailed(ctx context.Context) (int64, error)
	ResetStuck(ctx context.Context) (int64, error)
	RetryAll(ctx context.Context) (int64, error)
	RetryIDs(ctx context.Context, ids []int64) (queueRetryResult, error)
	Health(ctx context.Context) (queueHealthView, error)
}

type queueIPCFacade struct {
	client *ipc.Client
}

type queueStoreFacade struct {
	store   *queue.Store
	service *api.QueueService
}

func (f *queueStoreFacade) queueService() *api.QueueService {
	if f.service == nil && f.store != nil {
		f.service = api.NewQueueService(f.store)
	}
	return f.service
}

func (f *queueIPCFacade) Stats(_ context.Context) (map[string]int, error) {
	resp, err := f.client.Status()
	if err != nil {
		return nil, err
	}
	stats := make(map[string]int, len(resp.QueueStats))
	for key, value := range resp.QueueStats {
		stats[key] = value
	}
	return stats, nil
}

func (f *queueIPCFacade) List(_ context.Context, statuses []string) ([]queueItemView, error) {
	resp, err := f.client.QueueList(statuses)
	if err != nil {
		return nil, err
	}
	items := make([]queueItemView, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, queueItemView{
			ID:              item.ID,
			DiscTitle:       item.DiscTitle,
			SourcePath:      item.SourcePath,
			Status:          item.Status,
			CreatedAt:       item.CreatedAt,
			DiscFingerprint: item.DiscFingerprint,
		})
	}
	return items, nil
}

func (f *queueIPCFacade) Describe(_ context.Context, id int64) (*queueItemDetailsView, error) {
	resp, err := f.client.QueueDescribe(id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil, nil
		}
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	view := convertDTOQueueItem(resp.Item)
	return &view, nil
}

func (f *queueIPCFacade) ClearAll(_ context.Context) (int64, error) {
	resp, err := f.client.QueueClear()
	if err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

func (f *queueIPCFacade) ClearCompleted(_ context.Context) (int64, error) {
	resp, err := f.client.QueueClearCompleted()
	if err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

func (f *queueIPCFacade) ClearFailed(_ context.Context) (int64, error) {
	resp, err := f.client.QueueClearFailed()
	if err != nil {
		return 0, err
	}
	return resp.Removed, nil
}

func (f *queueIPCFacade) ResetStuck(_ context.Context) (int64, error) {
	resp, err := f.client.QueueReset()
	if err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

func (f *queueIPCFacade) RetryAll(_ context.Context) (int64, error) {
	resp, err := f.client.QueueRetry(nil)
	if err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

func (f *queueIPCFacade) RetryIDs(_ context.Context, ids []int64) (queueRetryResult, error) {
	result := queueRetryResult{
		Items: make([]queueRetryItemResult, 0, len(ids)),
	}

	resp, err := f.client.QueueList(nil)
	if err != nil {
		return queueRetryResult{}, err
	}

	itemsByID := make(map[int64]ipc.QueueItem, len(resp.Items))
	for _, item := range resp.Items {
		itemsByID[item.ID] = item
	}

	for _, id := range ids {
		item, ok := itemsByID[id]
		if !ok {
			result.Items = append(result.Items, queueRetryItemResult{ID: id, Outcome: queueRetryOutcomeNotFound})
			continue
		}
		if !statusIsFailed(item.Status) {
			result.Items = append(result.Items, queueRetryItemResult{ID: id, Outcome: queueRetryOutcomeNotFailed})
			continue
		}

		retryResp, err := f.client.QueueRetry([]int64{id})
		if err != nil {
			return queueRetryResult{}, err
		}
		if retryResp != nil && retryResp.Updated > 0 {
			result.UpdatedCount += retryResp.Updated
			result.Items = append(result.Items, queueRetryItemResult{ID: id, Outcome: queueRetryOutcomeUpdated})
			continue
		}

		result.Items = append(result.Items, queueRetryItemResult{ID: id, Outcome: queueRetryOutcomeNotFailed})
	}

	return result, nil
}

func (f *queueIPCFacade) Health(_ context.Context) (queueHealthView, error) {
	resp, err := f.client.QueueHealth()
	if err != nil {
		return queueHealthView{}, err
	}
	return queueHealthView{
		Total:      resp.Total,
		Pending:    resp.Pending,
		Processing: resp.Processing,
		Failed:     resp.Failed,
		Review:     resp.Review,
		Completed:  resp.Completed,
	}, nil
}

func (f *queueStoreFacade) Stats(ctx context.Context) (map[string]int, error) {
	svc := f.queueService()
	if svc == nil {
		return nil, nil
	}
	stats, err := svc.Stats(ctx)
	if err != nil {
		return nil, err
	}
	return stats, nil
}

func (f *queueStoreFacade) List(ctx context.Context, statuses []string) ([]queueItemView, error) {
	var filters []queue.Status
	for _, status := range statuses {
		parsed, ok := queue.ParseStatus(status)
		if !ok {
			continue
		}
		filters = append(filters, parsed)
	}

	svc := f.queueService()
	if svc == nil {
		return nil, nil
	}
	items, err := svc.List(ctx, filters...)
	if err != nil {
		return nil, err
	}

	views := make([]queueItemView, 0, len(items))
	for _, item := range items {
		views = append(views, queueItemView{
			ID:              item.ID,
			DiscTitle:       item.DiscTitle,
			SourcePath:      item.SourcePath,
			Status:          item.Status,
			CreatedAt:       item.CreatedAt,
			DiscFingerprint: item.DiscFingerprint,
		})
	}
	return views, nil
}

func (f *queueStoreFacade) Describe(ctx context.Context, id int64) (*queueItemDetailsView, error) {
	svc := f.queueService()
	if svc == nil {
		return nil, nil
	}
	item, err := svc.Describe(ctx, id)
	if err != nil || item == nil {
		return nil, err
	}
	view := convertDTOQueueItem(*item)
	return &view, nil
}

func (f *queueStoreFacade) ClearAll(ctx context.Context) (int64, error) {
	return f.store.Clear(ctx)
}

func (f *queueStoreFacade) ClearCompleted(ctx context.Context) (int64, error) {
	return f.store.ClearCompleted(ctx)
}

func (f *queueStoreFacade) ClearFailed(ctx context.Context) (int64, error) {
	return f.store.ClearFailed(ctx)
}

func (f *queueStoreFacade) ResetStuck(ctx context.Context) (int64, error) {
	return f.store.ResetStuckProcessing(ctx)
}

func (f *queueStoreFacade) RetryAll(ctx context.Context) (int64, error) {
	return f.store.RetryFailed(ctx)
}

func (f *queueStoreFacade) RetryIDs(ctx context.Context, ids []int64) (queueRetryResult, error) {
	result := queueRetryResult{
		Items: make([]queueRetryItemResult, 0, len(ids)),
	}

	for _, id := range ids {
		item, err := f.store.GetByID(ctx, id)
		if err != nil {
			return queueRetryResult{}, err
		}
		if item == nil {
			result.Items = append(result.Items, queueRetryItemResult{ID: id, Outcome: queueRetryOutcomeNotFound})
			continue
		}
		if item.Status != queue.StatusFailed {
			result.Items = append(result.Items, queueRetryItemResult{ID: id, Outcome: queueRetryOutcomeNotFailed})
			continue
		}

		updated, err := f.store.RetryFailed(ctx, id)
		if err != nil {
			return queueRetryResult{}, err
		}
		if updated > 0 {
			result.UpdatedCount += updated
			result.Items = append(result.Items, queueRetryItemResult{ID: id, Outcome: queueRetryOutcomeUpdated})
			continue
		}

		result.Items = append(result.Items, queueRetryItemResult{ID: id, Outcome: queueRetryOutcomeNotFailed})
	}

	return result, nil
}

func (f *queueStoreFacade) Health(ctx context.Context) (queueHealthView, error) {
	summary, err := f.store.Health(ctx)
	if err != nil {
		return queueHealthView{}, err
	}
	return queueHealthView{
		Total:      summary.Total,
		Pending:    summary.Pending,
		Processing: summary.Processing,
		Failed:     summary.Failed,
		Review:     summary.Review,
		Completed:  summary.Completed,
	}, nil
}

func statusIsFailed(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), string(queue.StatusFailed))
}

func convertDTOQueueItem(item api.QueueItem) queueItemDetailsView {
	base := queueItemView{
		ID:              item.ID,
		DiscTitle:       item.DiscTitle,
		SourcePath:      item.SourcePath,
		Status:          item.Status,
		CreatedAt:       item.CreatedAt,
		DiscFingerprint: item.DiscFingerprint,
	}
	view := queueItemDetailsView{
		queueItemView:     base,
		UpdatedAt:         item.UpdatedAt,
		ProgressStage:     item.Progress.Stage,
		ProgressPercent:   item.Progress.Percent,
		ProgressMessage:   item.Progress.Message,
		DraptoPreset:      item.DraptoPresetProfile,
		ErrorMessage:      item.ErrorMessage,
		NeedsReview:       item.NeedsReview,
		ReviewReason:      item.ReviewReason,
		MetadataJSON:      string(item.Metadata),
		RipSpecJSON:       string(item.RipSpec),
		RippedFile:        item.RippedFile,
		EncodedFile:       item.EncodedFile,
		FinalFile:         item.FinalFile,
		BackgroundLogPath: item.BackgroundLogPath,
	}
	if len(item.Episodes) > 0 {
		view.Episodes, view.EpisodeTotals = convertAPIEpisodes(item)
		view.EpisodesSynced = item.EpisodesSynced
	} else {
		view.EpisodesSynced = item.EpisodesSynced
	}
	return view
}

func convertAPIEpisodes(item api.QueueItem) ([]queueEpisodeView, queueEpisodeTotals) {
	episodes := make([]queueEpisodeView, 0, len(item.Episodes))
	for _, ep := range item.Episodes {
		view := queueEpisodeView{
			Key:              ep.Key,
			Season:           ep.Season,
			Episode:          ep.Episode,
			Title:            strings.TrimSpace(ep.Title),
			Stage:            strings.TrimSpace(ep.Stage),
			RuntimeSeconds:   ep.RuntimeSeconds,
			RippedPath:       strings.TrimSpace(ep.RippedPath),
			EncodedPath:      strings.TrimSpace(ep.EncodedPath),
			FinalPath:        strings.TrimSpace(ep.FinalPath),
			SubtitleSource:   strings.TrimSpace(ep.SubtitleSource),
			SubtitleLanguage: strings.TrimSpace(ep.SubtitleLanguage),
			MatchScore:       ep.MatchScore,
		}
		if view.Title == "" {
			view.Title = strings.TrimSpace(ep.OutputBasename)
		}
		if view.Stage == "" {
			view.Stage = "planned"
		}
		episodes = append(episodes, view)
	}
	totals := queueEpisodeTotals{}
	if item.EpisodeTotals != nil {
		totals.Planned = item.EpisodeTotals.Planned
		totals.Ripped = item.EpisodeTotals.Ripped
		totals.Encoded = item.EpisodeTotals.Encoded
		totals.Final = item.EpisodeTotals.Final
	} else {
		totals = tallyEpisodeTotals(episodes)
	}
	return episodes, totals
}

func tallyEpisodeTotals(episodes []queueEpisodeView) queueEpisodeTotals {
	var totals queueEpisodeTotals
	for _, ep := range episodes {
		totals.Planned++
		if ep.RippedPath != "" {
			totals.Ripped++
		}
		if ep.EncodedPath != "" {
			totals.Encoded++
		}
		if ep.FinalPath != "" {
			totals.Final++
		}
	}
	return totals
}
