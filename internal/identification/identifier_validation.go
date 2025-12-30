package identification

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
)

func (i *Identifier) validateIdentification(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, i.logger)
	fingerprint := strings.TrimSpace(item.DiscFingerprint)
	if fingerprint == "" {
		logger.Error("identification validation failed",
			logging.String("reason", "missing fingerprint"),
			logging.String(logging.FieldErrorKind, string(services.ErrorKindValidation)),
			logging.String(logging.FieldErrorOperation, "validate fingerprint"),
			logging.String("error_message", "Disc fingerprint missing after identification"),
			logging.String(logging.FieldErrorHint, "Rerun identification to capture MakeMKV scan results"))
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"validate fingerprint",
			"Disc fingerprint missing after identification; rerun identification to capture MakeMKV scan results",
			nil,
		)
	}

	ripSpecRaw := strings.TrimSpace(item.RipSpecData)
	if ripSpecRaw == "" {
		logger.Error("identification validation failed",
			logging.String("reason", "missing rip spec"),
			logging.String(logging.FieldErrorKind, string(services.ErrorKindValidation)),
			logging.String(logging.FieldErrorOperation, "validate rip spec"),
			logging.String("error_message", "Rip specification missing after identification"),
			logging.String(logging.FieldErrorHint, "Rerun identification to rebuild the rip spec"))
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"validate rip spec",
			"Rip specification missing after identification; unable to determine ripping instructions",
			nil,
		)
	}

	spec, err := ripspec.Parse(ripSpecRaw)
	if err != nil {
		logger.Error("identification validation failed",
			logging.String("reason", "invalid rip spec"),
			logging.Error(err),
			logging.String(logging.FieldErrorKind, string(services.ErrorKindValidation)),
			logging.String(logging.FieldErrorOperation, "parse rip spec"),
			logging.String("error_message", "Rip specification JSON failed to parse"),
			logging.String(logging.FieldErrorHint, "Rerun identification to regenerate the rip spec"))
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"parse rip spec",
			"Rip specification is invalid JSON; cannot continue",
			err,
		)
	}
	if specFingerprint := strings.TrimSpace(spec.Fingerprint); !strings.EqualFold(specFingerprint, fingerprint) {
		logger.Error(
			"identification validation failed",
			logging.String("reason", "fingerprint mismatch"),
			logging.String("item_fingerprint", fingerprint),
			logging.String("spec_fingerprint", specFingerprint),
			logging.String(logging.FieldErrorKind, string(services.ErrorKindValidation)),
			logging.String(logging.FieldErrorOperation, "validate rip spec fingerprint"),
			logging.String("error_message", "Rip specification fingerprint does not match queue item"),
			logging.String(logging.FieldErrorHint, "Rerun identification to regenerate the rip spec"),
		)
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"validate rip spec fingerprint",
			"Rip specification fingerprint does not match queue item fingerprint",
			nil,
		)
	}

	if err := i.ensureStagingSkeleton(item); err != nil {
		return err
	}

	logger.Debug(
		"identification validation succeeded",
		logging.String("fingerprint", fingerprint),
	)

	return nil
}

// logStageSummary logs a summary of the identification stage with timing and key metrics.
func (i *Identifier) logStageSummary(ctx context.Context, item *queue.Item, stageStart time.Time, identified bool, titleCount int, tmdbID int64, mediaType string) {
	logger := logging.WithContext(ctx, i.logger)
	attrs := []logging.Attr{
		logging.String(logging.FieldEventType, "stage_complete"),
		logging.Duration("stage_duration", time.Since(stageStart)),
		logging.Bool("identified", identified),
		logging.Int("title_count", titleCount),
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
	}
	if identified {
		attrs = append(attrs, logging.Int64("tmdb_id", tmdbID))
		attrs = append(attrs, logging.String("media_type", mediaType))
	}
	if item.NeedsReview {
		attrs = append(attrs, logging.Bool("needs_review", true))
		attrs = append(attrs, logging.String("review_reason", strings.TrimSpace(item.ReviewReason)))
	}
	logger.Info("identification stage summary", logging.Args(attrs...)...)
}

func (i *Identifier) ensureStagingSkeleton(item *queue.Item) error {
	if i.cfg == nil {
		return services.Wrap(
			services.ErrConfiguration,
			"identification",
			"resolve configuration",
			"Configuration unavailable; cannot allocate staging directory",
			nil,
		)
	}
	base := strings.TrimSpace(i.cfg.Paths.StagingDir)
	if base == "" {
		return services.Wrap(
			services.ErrConfiguration,
			"identification",
			"resolve staging dir",
			"staging_dir is empty; configure staging directories before ripping",
			nil,
		)
	}
	root := strings.TrimSpace(item.StagingRoot(base))
	if root == "" {
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"determine staging root",
			"Unable to determine staging directory for fingerprint",
			nil,
		)
	}
	for _, sub := range []string{"", "rips", "encoded", "organizing"} {
		path := root
		if sub != "" {
			path = filepath.Join(root, sub)
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return services.Wrap(
				services.ErrConfiguration,
				"identification",
				"create staging directories",
				fmt.Sprintf("Failed to create staging directory %q", path),
				err,
			)
		}
	}
	return nil
}
