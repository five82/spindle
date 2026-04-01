package contentid

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/services"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/textutil"
	"github.com/five82/spindle/internal/transcription"
)

const (
	minSimilarityScore           = 0.58
	lowConfidenceReviewThreshold = 0.70
	llmVerifyThreshold           = 0.85
)

// Handler implements stage.Handler for episode identification.
type Handler struct {
	cfg         *config.Config
	store       *queue.Store
	llmClient   *llm.Client
	osClient    *opensubtitles.Client
	transcriber *transcription.Service
}

// New creates an episode identification handler.
func New(
	cfg *config.Config,
	store *queue.Store,
	llmClient *llm.Client,
	osClient *opensubtitles.Client,
	transcriber *transcription.Service,
) *Handler {
	return &Handler{
		cfg:         cfg,
		store:       store,
		llmClient:   llmClient,
		osClient:    osClient,
		transcriber: transcriber,
	}
}

// Compile-time check that Handler implements stage.Handler.
var _ stage.Handler = (*Handler)(nil)

// Run executes the episode identification stage.
// Returns immediately for movies (no-op).
func (h *Handler) Run(ctx context.Context, item *queue.Item) error {
	logger := stage.LoggerFromContext(ctx)

	env, err := stage.ParseRipSpec(item.RipSpecData)
	if err != nil {
		return err
	}

	// Movies skip episode identification.
	if env.Metadata.MediaType == "movie" {
		logger.Info("skipping episode identification for movie",
			"decision_type", logs.DecisionEpisodeIDSkip,
			"decision_result", "skipped",
			"decision_reason", "media type is movie",
		)
		return nil
	}

	logger.Info("episode identification stage started", "event_type", "stage_start", "stage", "episode_identification")

	// Step 1: Transcribe each ripped episode.
	item.ProgressPercent = 10
	item.ProgressMessage = "Phase 1/3 - Transcribing episodes"
	_ = h.store.UpdateProgress(item)

	episodeCount := len(env.Episodes)
	discFPs := make(map[string]*textutil.Fingerprint)
	for i, ep := range env.Episodes {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		asset, ok := env.Assets.FindAsset("ripped", ep.Key)
		if !ok || !asset.IsCompleted() {
			continue
		}

		item.ActiveEpisodeKey = ep.Key
		item.ProgressPercent = 10 + (40 * float64(i+1) / float64(episodeCount))
		item.ProgressMessage = fmt.Sprintf("Phase 1/3 - Transcribing (%s)", ep.Key)
		_ = h.store.UpdateProgress(item)

		contentKey := fmt.Sprintf("%s:%s:0", item.DiscFingerprint, ep.Key)
		outputDir := filepath.Join(os.TempDir(), fmt.Sprintf("spindle-contentid-%s-%s", item.DiscFingerprint, ep.Key))
		result, err := h.transcriber.Transcribe(ctx, transcription.TranscribeRequest{
			InputPath:  asset.Path,
			AudioIndex: 0,
			Language:   "en",
			OutputDir:  outputDir,
			ContentKey: contentKey,
		})
		if err != nil {
			return fmt.Errorf("transcribe %s: %w", ep.Key, err)
		}

		fp := textutil.NewFingerprint(readSRTText(result.SRTPath))
		if fp != nil {
			discFPs[ep.Key] = fp
		}
	}

	if len(discFPs) == 0 {
		logger.Warn("no valid transcriptions for episode ID",
			"event_type", "episode_id_no_transcripts",
			"error_hint", "all transcriptions produced empty fingerprints",
			"impact", "episodes remain unresolved",
		)
		item.AppendReviewReason("Episode ID: no valid transcriptions")
		return &services.ErrDegraded{Msg: "no valid transcriptions"}
	}

	// Step 2: Download reference subtitles from OpenSubtitles.
	item.ActiveEpisodeKey = ""
	item.ProgressPercent = 50
	item.ProgressMessage = "Phase 2/3 - Fetching reference subtitles"
	_ = h.store.UpdateProgress(item)

	refFPs, err := h.downloadReferences(ctx, logger, &env)
	if err != nil {
		logger.Warn("reference download failed",
			"event_type", "reference_download_error",
			"error_hint", err.Error(),
			"impact", "episodes remain unresolved",
		)
		item.AppendReviewReason("Episode ID: reference download failed")
		return &services.ErrDegraded{Msg: "reference download failed: " + err.Error()}
	}

	if len(refFPs) == 0 {
		item.AppendReviewReason("Episode ID: no reference subtitles found")
		return &services.ErrDegraded{Msg: "no reference subtitles found"}
	}

	// Step 3: Build IDF corpus and apply weights.
	corpus := &textutil.Corpus{}
	for _, fp := range discFPs {
		corpus.Add(fp)
	}
	for _, fp := range refFPs {
		corpus.Add(fp)
	}
	idf := corpus.IDF()

	weightedDisc := make(map[string]*textutil.Fingerprint)
	for k, fp := range discFPs {
		weightedDisc[k] = fp.WithIDF(idf)
	}
	weightedRef := make(map[int]*textutil.Fingerprint)
	for ep, fp := range refFPs {
		weightedRef[ep] = fp.WithIDF(idf)
	}

	// Step 4: Build similarity matrix and run Hungarian algorithm.
	item.ProgressPercent = 80
	item.ProgressMessage = "Phase 3/3 - Matching episodes"
	_ = h.store.UpdateProgress(item)

	matches := h.matchEpisodes(logger, weightedDisc, weightedRef, &env)

	// Step 5: Apply matches to envelope.
	h.applyMatches(logger, &env, matches, item)

	// Persist.
	if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
		return err
	}

	item.ProgressPercent = 95
	item.ProgressMessage = "Phase 3/3 - Episode identification complete"
	_ = h.store.UpdateProgress(item)

	logger.Info("episode identification stage completed", "event_type", "stage_complete", "stage", "episode_identification")
	return nil
}
