package identification

import (
	"context"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/disc"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/services"
)

func (i *Identifier) scanDisc(ctx context.Context) (*disc.ScanResult, error) {
	if i.scanner == nil {
		return nil, services.Wrap(
			services.ErrConfiguration,
			"identification",
			"initialize scanner",
			"Disc scanner unavailable; install MakeMKV and ensure it is in PATH",
			nil,
		)
	}
	device := strings.TrimSpace(i.cfg.MakeMKV.OpticalDrive)
	if device == "" {
		return nil, services.Wrap(
			services.ErrConfiguration,
			"identification",
			"resolve optical drive",
			"Optical drive path not configured; set optical_drive in spindle config to your MakeMKV drive identifier",
			nil,
		)
	}
	scanCtx := ctx
	if i.cfg.MakeMKV.InfoTimeout > 0 {
		var cancel context.CancelFunc
		scanCtx, cancel = context.WithTimeout(ctx, time.Duration(i.cfg.MakeMKV.InfoTimeout)*time.Second)
		defer cancel()
	}

	result, err := i.scanner.Scan(scanCtx, device)
	if err != nil {
		if scanCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return nil, services.Wrap(services.ErrExternalTool, "identification", "makemkv scan",
				fmt.Sprintf("MakeMKV disc info scan timed out after %ds; disc may have unreadable sectors", i.cfg.MakeMKV.InfoTimeout), err)
		}
		return nil, services.Wrap(services.ErrExternalTool, "identification", "makemkv scan", "MakeMKV disc scan failed", err)
	}
	return result, nil
}

// scanDiscAndCaptureFingerprint scans the disc for title info. The fingerprint should
// already be set by the daemon; this function validates it and handles duplicates.
func (i *Identifier) scanDiscAndCaptureFingerprint(ctx context.Context, item *queue.Item, logger *slog.Logger) (*disc.ScanResult, int, error) {
	device := strings.TrimSpace(i.cfg.MakeMKV.OpticalDrive)
	existingFingerprint := strings.TrimSpace(item.DiscFingerprint)

	logger.Debug("scanning disc with makemkv",
		logging.String("device", device),
		logging.String("existing_fingerprint", existingFingerprint),
		logging.String(logging.FieldEventType, "scan_start"))
	scanStart := time.Now()

	scanResult, err := i.scanDisc(ctx)
	if err != nil {
		return nil, 0, err
	}

	// Surface disc/hardware warnings detected during scan
	if scanResult != nil && len(scanResult.Warnings) > 0 {
		for _, w := range scanResult.Warnings {
			logger.Warn("disc error during scan",
				logging.String("event_type", "disc_scan_warning"),
				logging.String("error_hint", "disc may have physical damage or drive issue"),
				logging.String("impact", "scan may produce incomplete results"),
				logging.String("warning", w),
			)
		}
		logger.Warn("disc scan completed with errors",
			logging.String("event_type", "disc_scan_errors_summary"),
			logging.Int("warning_count", len(scanResult.Warnings)),
			logging.String("error_hint", "check disc for scratches or try cleaning"),
		)
	}

	titleCount := 0
	hasBDInfo := false
	if scanResult != nil {
		titleCount = len(scanResult.Titles)
		hasBDInfo = scanResult.BDInfo != nil
		if scanResult.BDInfo != nil {
			logger.Debug("bd_info details",
				logging.String("disc_id", strings.TrimSpace(scanResult.BDInfo.DiscID)),
				logging.String("volume_identifier", scanResult.BDInfo.VolumeIdentifier),
				logging.String("disc_name", scanResult.BDInfo.DiscName),
				logging.Bool("is_blu_ray", scanResult.BDInfo.IsBluRay),
				logging.Bool("has_aacs", scanResult.BDInfo.HasAACS))
		}
	}

	// Use existing fingerprint from daemon; it's mandatory at enqueue time
	if existingFingerprint == "" {
		return nil, 0, services.Wrap(
			services.ErrValidation,
			"identification",
			"validate fingerprint",
			"Disc fingerprint missing; should have been set at enqueue time",
			nil,
		)
	}
	logger.Debug("using fingerprint from daemon", logging.String("fingerprint", existingFingerprint))
	if err := i.handleDuplicateFingerprint(ctx, item); err != nil {
		return nil, 0, err
	}
	if item.Status == queue.StatusFailed {
		return scanResult, titleCount, nil
	}

	scanSummary := []logging.Attr{
		logging.Int("title_count", titleCount),
		logging.Bool("bd_info_available", hasBDInfo),
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
		logging.Duration("scan_duration", time.Since(scanStart)),
	}
	if fp := strings.TrimSpace(item.DiscFingerprint); fp != "" {
		scanSummary = append(scanSummary, logging.String("fingerprint", fp))
	}
	scanSummary = append(scanSummary, logging.String(logging.FieldEventType, "scan_complete"))
	logger.Debug("disc scan completed", logging.Args(scanSummary...)...)

	return scanResult, titleCount, nil
}
