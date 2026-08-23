package httpapi

import (
	"fmt"
	"strings"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

// RetryResult describes the outcome of a retry-episode request. It is the
// wire value of the /api/queue/retry-episode response's "result" field.
type RetryResult string

const (
	RetryResultRetried         RetryResult = "retried"
	RetryResultNotFound        RetryResult = "not_found"
	RetryResultNotFailed       RetryResult = "not_failed"
	RetryResultEpisodeNotFound RetryResult = "episode_not_found"
)

// retryEpisode clears the failed status of a single episode within a queue
// item and resets the item for reprocessing from its failed stage.
func retryEpisode(store *queue.Store, id int64, episodeKey string) (RetryResult, error) {
	item, err := store.GetByID(id)
	if err != nil {
		return "", fmt.Errorf("retry episode get %d: %w", id, err)
	}
	if item == nil {
		return RetryResultNotFound, nil
	}
	if item.Stage != queue.StageFailed {
		return RetryResultNotFailed, nil
	}
	if item.RipSpecData == "" {
		return RetryResultEpisodeNotFound, nil
	}

	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		return "", fmt.Errorf("retry episode parse ripspec %d: %w", id, err)
	}

	found := false
	for _, ep := range env.Episodes {
		if strings.EqualFold(ep.Key, episodeKey) {
			found = true
			break
		}
	}
	if !found {
		return RetryResultEpisodeNotFound, nil
	}

	for _, kind := range []string{ripspec.AssetKindEncoded, ripspec.AssetKindSubtitled, ripspec.AssetKindFinal} {
		env.Assets.ClearFailedAsset(kind, episodeKey)
	}

	encoded, err := env.Encode()
	if err != nil {
		return "", fmt.Errorf("retry episode encode ripspec %d: %w", id, err)
	}

	if err := store.RetryWithRipSpec(id, item.ResumeStage(), encoded); err != nil {
		return "", fmt.Errorf("retry episode update %d: %w", id, err)
	}
	return RetryResultRetried, nil
}
