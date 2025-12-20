package identification

import (
	"context"
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
	result, err := i.scanner.Scan(ctx, device)
	if err != nil {
		return nil, services.Wrap(services.ErrExternalTool, "identification", "makemkv scan", "MakeMKV disc scan failed", err)
	}
	return result, nil
}

// scanDiscAndCaptureFingerprint scans the disc, captures the fingerprint, and handles duplicates.
func (i *Identifier) scanDiscAndCaptureFingerprint(ctx context.Context, item *queue.Item, logger *slog.Logger) (*disc.ScanResult, int, error) {
	device := strings.TrimSpace(i.cfg.MakeMKV.OpticalDrive)
	logger.Info("scanning disc with makemkv", logging.String("device", device))
	scanStart := time.Now()

	scanResult, err := i.scanDisc(ctx)
	if err != nil {
		return nil, 0, err
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

	scannerFingerprint := ""
	if scanResult != nil {
		scannerFingerprint = strings.TrimSpace(scanResult.Fingerprint)
		if scannerFingerprint == "" && scanResult.BDInfo != nil {
			if discID := strings.TrimSpace(scanResult.BDInfo.DiscID); discID != "" {
				scannerFingerprint = strings.ToUpper(discID)
				scanResult.Fingerprint = scannerFingerprint
				logger.Info("using bd_info disc id as fingerprint", logging.String("fingerprint", scannerFingerprint))
			}
		}
	}

	if scannerFingerprint != "" {
		logger.Debug("disc fingerprint captured", logging.String("fingerprint", scannerFingerprint))
		item.DiscFingerprint = scannerFingerprint
		if err := i.handleDuplicateFingerprint(ctx, item); err != nil {
			return nil, 0, err
		}
		if item.Status == queue.StatusReview {
			return scanResult, titleCount, nil
		}
	} else if trimmed := strings.TrimSpace(item.DiscFingerprint); trimmed != "" {
		logger.Debug("scanner fingerprint unavailable; retaining existing fingerprint",
			logging.String("fingerprint", trimmed))
	} else {
		logger.Warn("scanner fingerprint unavailable and queue fingerprint missing")
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
	logger.Info("disc scan completed", logging.Args(scanSummary...)...)

	return scanResult, titleCount, nil
}
