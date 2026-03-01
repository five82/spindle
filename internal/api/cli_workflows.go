package api

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"spindle/internal/config"
	"spindle/internal/disc"
	"spindle/internal/disc/fingerprint"
	"spindle/internal/identification"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripcache"
	"spindle/internal/ripping"
	"spindle/internal/services/jellyfin"
	"spindle/internal/stageexec"
	"spindle/internal/subtitles"
)

type IdentifyDiscRequest struct {
	Config *config.Config
	Device string
	Logger *slog.Logger
}

type IdentifyDiscResult struct {
	Device    string
	DiscLabel string
	Item      *queue.Item
}

type IdentifyDiscAssessment struct {
	TMDBTitle       string
	Year            string
	Edition         string
	MetadataPresent bool
	LibraryFilename string
	ReviewRequired  bool
	ReviewReason    string
	Outcome         string
	OutcomeMessage  string
}

// AssessIdentifyDisc derives CLI-facing identification outcomes from queue state.
func AssessIdentifyDisc(item *queue.Item) IdentifyDiscAssessment {
	if item == nil {
		return IdentifyDiscAssessment{
			TMDBTitle:      "Unknown",
			Year:           "Unknown",
			Outcome:        "failed",
			OutcomeMessage: "❌ Identification failed. Check the logs above for details.",
		}
	}

	meta := parseMetadataFields(item.MetadataJSON)
	assessment := IdentifyDiscAssessment{
		TMDBTitle:       meta.title,
		Year:            meta.year,
		Edition:         meta.edition,
		MetadataPresent: strings.TrimSpace(item.MetadataJSON) != "",
		ReviewRequired:  item.NeedsReview,
		ReviewReason:    strings.TrimSpace(item.ReviewReason),
	}
	if meta.filename != "" {
		assessment.LibraryFilename = meta.filename + ".mkv"
	} else if assessment.Year != "Unknown" && assessment.TMDBTitle != "Unknown" {
		assessment.LibraryFilename = fmt.Sprintf("%s (%s).mkv", assessment.TMDBTitle, assessment.Year)
	}

	switch {
	case assessment.MetadataPresent && !assessment.ReviewRequired:
		assessment.Outcome = "success"
		assessment.OutcomeMessage = "🎬 Identification successful! Disc would proceed to ripping stage."
	case assessment.ReviewRequired:
		assessment.Outcome = "review"
		assessment.OutcomeMessage = "⚠️  Identification requires manual review. Check the logs above for details."
	default:
		assessment.Outcome = "failed"
		assessment.OutcomeMessage = "❌ Identification failed. Check the logs above for details."
	}

	return assessment
}

func IdentifyDisc(ctx context.Context, req IdentifyDiscRequest) (IdentifyDiscResult, error) {
	cfg := req.Config
	if cfg == nil {
		return IdentifyDiscResult{}, fmt.Errorf("configuration is required")
	}
	logger := req.Logger
	if logger == nil {
		logger = logging.NewNop()
	}

	device := strings.TrimSpace(req.Device)
	if device == "" {
		device = strings.TrimSpace(cfg.MakeMKV.OpticalDrive)
	}
	if device == "" {
		return IdentifyDiscResult{}, fmt.Errorf("no device specified and no optical_drive configured")
	}
	cfg.MakeMKV.OpticalDrive = device

	tmdbClient, err := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
	if err != nil {
		logger.Warn("tmdb client initialization failed",
			logging.Error(err),
			logging.String(logging.FieldEventType, "tmdb_client_init_failed"),
			logging.String(logging.FieldErrorHint, "verify tmdb_api_key in config"),
			logging.String(logging.FieldImpact, "identification cannot proceed"),
		)
		return IdentifyDiscResult{}, fmt.Errorf("create TMDB client: %w", err)
	}

	scanner := disc.NewScanner(cfg.MakemkvBinary())
	identifier := identification.NewIdentifierWithDependencies(cfg, nil, logger, tmdbClient, scanner, nil)

	logger.Debug("getting disc label", logging.String("device", device))
	discLabel, err := disc.ReadLabel(ctx, device, 10*time.Second)
	if err != nil {
		logger.Warn("failed to get disc label",
			logging.Error(err),
			logging.String(logging.FieldEventType, "disc_label_read_failed"),
			logging.String(logging.FieldErrorHint, "verify disc is inserted and readable"),
			logging.String(logging.FieldImpact, "identification may use fallback title"),
		)
		discLabel = ""
	} else {
		logger.Debug("disc label retrieved", logging.String("device", device), logging.String("label", discLabel))
	}
	logger.Info("detected disc label", logging.String("label", discLabel))

	item := &queue.Item{
		DiscTitle:  discLabel,
		SourcePath: "",
		Status:     queue.StatusPending,
	}

	overallTimeout := time.Duration(cfg.MakeMKV.InfoTimeout)*time.Second + 5*time.Minute
	identifyCtx, cancel := context.WithTimeout(ctx, overallTimeout)
	defer cancel()

	computedFingerprint, fpErr := fingerprint.ComputeTimeout(identifyCtx, device, "", 2*time.Minute)
	if fpErr != nil {
		logger.Warn("fingerprint computation failed",
			logging.Error(fpErr),
			logging.String(logging.FieldEventType, "fingerprint_compute_failed"),
			logging.String(logging.FieldErrorHint, "verify disc is readable and not copy-protected"),
			logging.String(logging.FieldImpact, "rip cache lookup will not work"),
		)
	} else if strings.TrimSpace(computedFingerprint) != "" {
		item.DiscFingerprint = strings.TrimSpace(computedFingerprint)
		logger.Info("fingerprint computed", logging.String("fingerprint", item.DiscFingerprint))
	}

	if err := identifier.Prepare(identifyCtx, item); err != nil {
		return IdentifyDiscResult{}, fmt.Errorf("prepare identification: %w", err)
	}
	if err := identifier.Execute(identifyCtx, item); err != nil {
		return IdentifyDiscResult{}, fmt.Errorf("execute identification: %w", err)
	}

	return IdentifyDiscResult{
		Device:    device,
		DiscLabel: discLabel,
		Item:      item,
	}, nil
}

type PopulateRipCacheRequest struct {
	Config *config.Config
	Device string
	Logger *slog.Logger
}

type PopulateRipCacheResult struct {
	CacheDir string
}

func PopulateRipCache(ctx context.Context, req PopulateRipCacheRequest) (PopulateRipCacheResult, error) {
	cfg := req.Config
	if cfg == nil {
		return PopulateRipCacheResult{}, fmt.Errorf("configuration is required")
	}
	logger := req.Logger
	if logger == nil {
		logger = logging.NewNop()
	}

	device := strings.TrimSpace(req.Device)
	if device == "" {
		device = strings.TrimSpace(cfg.MakeMKV.OpticalDrive)
	}
	if device == "" {
		return PopulateRipCacheResult{}, fmt.Errorf("no device specified and no optical_drive configured")
	}
	cfg.MakeMKV.OpticalDrive = device

	cacheManager := ripcache.NewManager(cfg, logger)
	if cacheManager == nil {
		return PopulateRipCacheResult{}, fmt.Errorf("rip cache is disabled or misconfigured (set rip_cache.enabled = true and configure rip_cache.dir/max_gib)")
	}
	notifier := notifications.NewService(cfg)

	discLabel, err := disc.ReadLabel(ctx, device, 10*time.Second)
	if err != nil {
		logger.Warn("failed to get disc label",
			logging.Error(err),
			logging.String(logging.FieldEventType, "disc_label_read_failed"),
			logging.String(logging.FieldErrorHint, "verify disc is inserted and readable"),
			logging.String(logging.FieldImpact, "disc fingerprint used without label"),
		)
		discLabel = ""
	}
	logger.Info("detected disc label", logging.String("label", discLabel))

	fpCtx, fpCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer fpCancel()
	discFingerprint, err := fingerprint.ComputeTimeout(fpCtx, device, "", 2*time.Minute)
	if err != nil {
		return PopulateRipCacheResult{}, fmt.Errorf("compute disc fingerprint: %w", err)
	}
	discFingerprint = strings.TrimSpace(discFingerprint)
	if discFingerprint == "" {
		return PopulateRipCacheResult{}, fmt.Errorf("disc fingerprint missing; verify the disc is readable")
	}

	store, err := queue.Open(cfg)
	if err != nil {
		return PopulateRipCacheResult{}, fmt.Errorf("open queue store: %w", err)
	}
	defer store.Close()

	item, err := store.NewDisc(ctx, discLabel, discFingerprint)
	if err != nil {
		return PopulateRipCacheResult{}, fmt.Errorf("create queue item: %w", err)
	}
	baseCtx := logging.WithItemID(ctx, item.ID)

	tmdbClient, err := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
	if err != nil {
		logger.Warn("tmdb client initialization failed",
			logging.Error(err),
			logging.String(logging.FieldEventType, "tmdb_client_init_failed"),
			logging.String(logging.FieldErrorHint, "verify tmdb_api_key in config"),
			logging.String(logging.FieldImpact, "identification will fail"),
		)
		return PopulateRipCacheResult{}, fmt.Errorf("create TMDB client: %w", err)
	}
	scanner := disc.NewScanner(cfg.MakemkvBinary())
	identifier := identification.NewIdentifierWithDependencies(cfg, nil, logger, tmdbClient, scanner, notifier)
	ripper := ripping.NewRipper(cfg, store, logger, notifier)

	if err := stageexec.Run(baseCtx, stageexec.Options{
		Logger:     logger,
		Store:      store,
		Notifier:   notifier,
		Handler:    identifier,
		StageName:  "identifier",
		Processing: queue.StatusIdentifying,
		Done:       queue.StatusIdentified,
		Item:       item,
	}); err != nil {
		return PopulateRipCacheResult{}, err
	}
	if item.Status == queue.StatusFailed {
		if item.NeedsReview {
			return PopulateRipCacheResult{}, fmt.Errorf("identification requires review: %s", strings.TrimSpace(item.ReviewReason))
		}
		return PopulateRipCacheResult{}, fmt.Errorf("identification failed: %s", strings.TrimSpace(item.ErrorMessage))
	}

	cacheDir := cacheManager.Path(item)
	if strings.TrimSpace(cacheDir) == "" {
		return PopulateRipCacheResult{}, fmt.Errorf("rip cache path unavailable")
	}
	if err := os.RemoveAll(cacheDir); err != nil {
		return PopulateRipCacheResult{}, fmt.Errorf("remove existing rip cache entry: %w", err)
	}

	if err := stageexec.Run(baseCtx, stageexec.Options{
		Logger:     logger,
		Store:      store,
		Notifier:   notifier,
		Handler:    ripper,
		StageName:  "ripper",
		Processing: queue.StatusRipping,
		Done:       queue.StatusRipped,
		Item:       item,
	}); err != nil {
		return PopulateRipCacheResult{}, err
	}
	if item.Status == queue.StatusFailed {
		if item.NeedsReview {
			return PopulateRipCacheResult{}, fmt.Errorf("ripping requires review: %s", strings.TrimSpace(item.ReviewReason))
		}
		return PopulateRipCacheResult{}, fmt.Errorf("ripping failed: %s", strings.TrimSpace(item.ErrorMessage))
	}

	if _, ok, err := ripcache.LoadMetadata(cacheDir); err != nil {
		return PopulateRipCacheResult{}, fmt.Errorf("load rip cache metadata: %w", err)
	} else if !ok {
		return PopulateRipCacheResult{}, fmt.Errorf("rip cache metadata missing; check rip_cache_dir permissions and free space")
	}

	if removed, err := store.Remove(baseCtx, item.ID); err != nil {
		logger.Warn("failed to remove queue item after cache populate",
			logging.Error(err),
			logging.String(logging.FieldEventType, "queue_item_remove_failed"),
			logging.String(logging.FieldErrorHint, "run spindle queue clear to clean up"),
			logging.String(logging.FieldImpact, "orphaned queue item may remain"),
		)
	} else if removed {
		logger.Info("removed queue item after cache populate", logging.Int64(logging.FieldItemID, item.ID))
	}

	return PopulateRipCacheResult{CacheDir: cacheDir}, nil
}

type QueueCachedEntryRequest struct {
	Config         *config.Config
	EntryNumber    int
	AllowDuplicate bool
}

type QueueCachedEntryResult struct {
	ItemID    int64
	DiscTitle string
}

func QueueCachedEntryForProcessing(ctx context.Context, req QueueCachedEntryRequest) (QueueCachedEntryResult, error) {
	cfg := req.Config
	if cfg == nil {
		return QueueCachedEntryResult{}, fmt.Errorf("configuration is required")
	}
	if req.EntryNumber < 1 {
		return QueueCachedEntryResult{}, fmt.Errorf("invalid entry number: %d (must be a positive integer)", req.EntryNumber)
	}

	manager := ripcache.NewManager(cfg, logging.NewNop())
	if manager == nil {
		return QueueCachedEntryResult{}, fmt.Errorf("rip cache is disabled or misconfigured")
	}
	stats, err := manager.Stats(ctx)
	if err != nil {
		return QueueCachedEntryResult{}, err
	}
	if req.EntryNumber > len(stats.EntrySummaries) {
		return QueueCachedEntryResult{}, fmt.Errorf("entry number %d out of range (only %d entries exist)", req.EntryNumber, len(stats.EntrySummaries))
	}

	entry := stats.EntrySummaries[req.EntryNumber-1]
	meta, ok, err := ripcache.LoadMetadata(entry.Directory)
	if err != nil {
		return QueueCachedEntryResult{}, err
	}
	if !ok {
		return QueueCachedEntryResult{}, fmt.Errorf("cache entry %d is missing metadata; re-rip to repopulate identification data", req.EntryNumber)
	}

	fingerprint := strings.TrimSpace(meta.DiscFingerprint)
	if fingerprint == "" {
		return QueueCachedEntryResult{}, fmt.Errorf("cache entry %d metadata is missing disc fingerprint", req.EntryNumber)
	}
	if strings.TrimSpace(meta.RipSpecData) == "" {
		return QueueCachedEntryResult{}, fmt.Errorf("cache entry %d metadata is missing rip spec data", req.EntryNumber)
	}
	if strings.TrimSpace(meta.MetadataJSON) == "" {
		return QueueCachedEntryResult{}, fmt.Errorf("cache entry %d metadata is missing TMDB metadata", req.EntryNumber)
	}

	discTitle := strings.TrimSpace(meta.DiscTitle)
	if discTitle == "" {
		parsed := queue.MetadataFromJSON(meta.MetadataJSON, "")
		discTitle = strings.TrimSpace(parsed.Title())
	}
	if discTitle == "" {
		discTitle = "Cached Disc"
	}

	store, err := queue.Open(cfg)
	if err != nil {
		return QueueCachedEntryResult{}, fmt.Errorf("open queue store: %w", err)
	}
	defer store.Close()

	if existing, err := store.FindByFingerprint(ctx, fingerprint); err != nil {
		return QueueCachedEntryResult{}, fmt.Errorf("check existing queue item: %w", err)
	} else if existing != nil && !req.AllowDuplicate {
		return QueueCachedEntryResult{}, fmt.Errorf("disc fingerprint already queued as item %d (status %s); use --allow-duplicate to add another", existing.ID, existing.Status)
	}

	item, err := store.NewDisc(ctx, discTitle, fingerprint)
	if err != nil {
		return QueueCachedEntryResult{}, fmt.Errorf("create queue item: %w", err)
	}
	item.RipSpecData = meta.RipSpecData
	item.MetadataJSON = meta.MetadataJSON
	item.NeedsReview = meta.NeedsReview
	item.ReviewReason = meta.ReviewReason
	if meta.NeedsReview {
		item.Status = queue.StatusFailed
		item.ProgressStage = "Failed"
		item.ProgressPercent = 100
		item.ProgressMessage = strings.TrimSpace(meta.ReviewReason)
		item.ErrorMessage = strings.TrimSpace(meta.ReviewReason)
	} else {
		item.Status = queue.StatusIdentified
		item.ProgressStage = "Identified"
		item.ProgressPercent = 100
		item.ProgressMessage = "Identified from rip cache"
	}

	if err := store.Update(ctx, item); err != nil {
		return QueueCachedEntryResult{}, fmt.Errorf("update queue item: %w", err)
	}

	return QueueCachedEntryResult{
		ItemID:    item.ID,
		DiscTitle: discTitle,
	}, nil
}

type GenerateSubtitlesRequest struct {
	Config      *config.Config
	Logger      *slog.Logger
	SourcePath  string
	OutputDir   string
	WorkDir     string
	FetchForced bool
	External    bool
}

type GenerateSubtitlesResult struct {
	SourcePath         string
	SubtitlePath       string
	ForcedSubtitlePath string
	Source             string
	SegmentCount       int
	Duration           time.Duration
	Muxed              bool
	MuxedTrackCount    int
}

func GenerateSubtitlesForFile(ctx context.Context, req GenerateSubtitlesRequest) (GenerateSubtitlesResult, error) {
	cfg := req.Config
	if cfg == nil {
		return GenerateSubtitlesResult{}, fmt.Errorf("configuration is required")
	}
	logger := req.Logger
	if logger == nil {
		logger = logging.NewNop()
	}

	source := strings.TrimSpace(req.SourcePath)
	if source == "" {
		return GenerateSubtitlesResult{}, fmt.Errorf("source file path is required")
	}
	source, _ = filepath.Abs(source)
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return GenerateSubtitlesResult{}, fmt.Errorf("source file %q not found", source)
		}
		return GenerateSubtitlesResult{}, fmt.Errorf("stat source: %w", err)
	}
	if info.IsDir() {
		return GenerateSubtitlesResult{}, fmt.Errorf("source path %q is a directory", source)
	}

	outDir := strings.TrimSpace(req.OutputDir)
	if outDir == "" {
		outDir = filepath.Dir(source)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return GenerateSubtitlesResult{}, fmt.Errorf("ensure output directory: %w", err)
	}

	workRoot := strings.TrimSpace(req.WorkDir)
	cleanupWorkDir := false
	if workRoot == "" {
		root := cfg.Paths.StagingDir
		if root == "" {
			root = os.TempDir()
		}
		tmp, err := os.MkdirTemp(root, "gensubtitle-")
		if err != nil {
			return GenerateSubtitlesResult{}, fmt.Errorf("create work directory: %w", err)
		}
		workRoot = tmp
		cleanupWorkDir = true
	}
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		if cleanupWorkDir {
			_ = os.RemoveAll(workRoot)
		}
		return GenerateSubtitlesResult{}, fmt.Errorf("ensure work directory: %w", err)
	}
	if cleanupWorkDir {
		defer os.RemoveAll(workRoot)
	}

	service := subtitles.NewService(cfg, logger)

	baseName := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	edition, _ := identification.ExtractKnownEdition(baseName)
	cleanedName := identification.StripEditionSuffix(baseName)
	inferredTitle, inferredYear := subtitles.SplitTitleAndYear(cleanedName)
	if inferredTitle == "" {
		inferredTitle = cleanedName
	}
	ctxMeta := subtitles.SubtitleContext{Title: inferredTitle, MediaType: "movie", Year: inferredYear, Edition: edition}
	if lang := strings.TrimSpace(cfg.TMDB.Language); lang != "" {
		ctxMeta.Language = strings.ToLower(strings.SplitN(lang, "-", 2)[0])
	}
	if edition != "" {
		logger.Info("detected edition from filename", logging.String("edition", edition))
	}

	if match := lookupTMDBMetadata(ctx, cfg, logger, inferredTitle, inferredYear); match != nil {
		ctxMeta.TMDBID = match.TMDBID
		ctxMeta.MediaType = match.MediaType
		if match.Title != "" {
			ctxMeta.Title = match.Title
		}
		if match.Year != "" {
			ctxMeta.Year = match.Year
		}
		logger.Info("tmdb metadata attached",
			logging.Int64("tmdb_id", match.TMDBID),
			logging.String("title", ctxMeta.Title),
			logging.String("year", ctxMeta.Year),
			logging.String("media_type", ctxMeta.MediaType),
		)
	} else {
		logger.Info("tmdb lookup skipped: no confident match", logging.String("title", inferredTitle))
	}

	languages := append([]string(nil), cfg.Subtitles.OpenSubtitlesLanguages...)
	result, err := service.Generate(ctx, subtitles.GenerateRequest{
		SourcePath:  source,
		WorkDir:     filepath.Join(workRoot, "work"),
		OutputDir:   outDir,
		BaseName:    baseName,
		FetchForced: req.FetchForced,
		Context:     ctxMeta,
		Languages:   languages,
	})
	if err != nil {
		return GenerateSubtitlesResult{}, fmt.Errorf("subtitle generation failed: %w", err)
	}

	out := GenerateSubtitlesResult{
		SourcePath:         source,
		SubtitlePath:       result.SubtitlePath,
		ForcedSubtitlePath: result.ForcedSubtitlePath,
		Source:             result.Source,
		SegmentCount:       result.SegmentCount,
		Duration:           result.Duration,
	}

	if !req.External && strings.HasSuffix(strings.ToLower(source), ".mkv") {
		var srtPaths []string
		if strings.TrimSpace(result.SubtitlePath) != "" {
			srtPaths = append(srtPaths, result.SubtitlePath)
		}
		if strings.TrimSpace(result.ForcedSubtitlePath) != "" {
			srtPaths = append(srtPaths, result.ForcedSubtitlePath)
		}
		if len(srtPaths) > 0 {
			lang := "en"
			if len(languages) > 0 {
				lang = languages[0]
			}
			muxer := subtitles.NewMuxer(logger)
			muxResult, muxErr := muxer.MuxSubtitles(ctx, subtitles.MuxRequest{
				MKVPath:           source,
				SubtitlePaths:     srtPaths,
				Language:          lang,
				StripExistingSubs: true,
			})
			if muxErr != nil {
				logger.Warn("subtitle muxing failed; keeping sidecar files",
					logging.Error(muxErr),
					logging.String("mkv_path", source),
				)
				return out, nil
			}
			out.Muxed = true
			out.MuxedTrackCount = len(muxResult.MuxedSubtitles)
		}
	}

	tryJellyfinRefresh(ctx, cfg, logger)
	return out, nil
}

func lookupTMDBMetadata(ctx context.Context, cfg *config.Config, logger *slog.Logger, title, year string) *identification.LookupMatch {
	if cfg == nil || strings.TrimSpace(cfg.TMDB.APIKey) == "" {
		return nil
	}
	client, err := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
	if err != nil {
		logger.Warn("tmdb client init failed",
			logging.Error(err),
			logging.String(logging.FieldEventType, "tmdb_client_init_failed"),
			logging.String(logging.FieldErrorHint, "verify tmdb_api_key in config"),
			logging.String(logging.FieldImpact, "subtitle context will lack TMDB metadata"),
		)
		return nil
	}
	opts := tmdb.SearchOptions{}
	if year != "" {
		if parsed, parseErr := strconv.Atoi(year); parseErr == nil {
			opts.Year = parsed
		}
	}
	match, err := identification.LookupTMDBByTitle(ctx, client, logger, title, opts)
	if err != nil {
		logger.Warn("tmdb lookup failed",
			logging.Error(err),
			logging.String("title", title),
			logging.String(logging.FieldEventType, "tmdb_lookup_failed"),
			logging.String(logging.FieldErrorHint, "verify title format or TMDB availability"),
			logging.String(logging.FieldImpact, "subtitle context will lack TMDB metadata"),
		)
		return nil
	}
	return match
}

func tryJellyfinRefresh(ctx context.Context, cfg *config.Config, logger *slog.Logger) {
	if cfg == nil || !cfg.Jellyfin.Enabled {
		return
	}
	jf := jellyfin.NewConfiguredService(cfg)
	if err := jf.Refresh(ctx, nil); err != nil {
		logger.Warn("jellyfin refresh failed",
			logging.Error(err),
			logging.String(logging.FieldEventType, "jellyfin_refresh_failed"),
			logging.String(logging.FieldErrorHint, "check jellyfin.url and jellyfin.api_key in config"),
		)
	} else {
		logger.Info("jellyfin library refresh requested",
			logging.String(logging.FieldEventType, "jellyfin_refresh_requested"),
		)
	}
}
