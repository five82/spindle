package api

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"spindle/internal/config"
	"spindle/internal/discidcache"
	"spindle/internal/logging"
	"spindle/internal/ripcache"
)

type OpenCacheResourceRequest struct {
	Config *config.Config
	Logger *slog.Logger
}

var (
	ErrRipCacheDisabled         = errors.New("rip cache is disabled")
	ErrRipCacheDirNotConfigured = errors.New("rip cache dir is not configured")
	ErrDiscIDCacheDisabled      = errors.New("disc ID cache is disabled")
	ErrDiscIDCacheNotConfigured = errors.New("disc ID cache path is not configured")
)

// OpenRipCacheManager validates config and initializes a rip cache manager.
func OpenRipCacheManager(cfg *config.Config, logger *slog.Logger) (*ripcache.Manager, error) {
	if cfg == nil || !cfg.RipCache.Enabled {
		return nil, ErrRipCacheDisabled
	}
	if strings.TrimSpace(cfg.RipCache.Dir) == "" {
		return nil, ErrRipCacheDirNotConfigured
	}
	if err := os.MkdirAll(cfg.RipCache.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("ensure cache dir: %w", err)
	}
	if logger == nil {
		logger = logging.NewNop()
	}
	manager := ripcache.NewManager(cfg, logger)
	if manager == nil {
		return nil, fmt.Errorf("initialize rip cache manager: invalid rip cache settings")
	}
	return manager, nil
}

// OpenDiscIDCache validates config and initializes the disc ID cache manager.
func OpenDiscIDCache(cfg *config.Config, logger *slog.Logger) (*discidcache.Cache, error) {
	if cfg == nil || !cfg.DiscIDCache.Enabled {
		return nil, ErrDiscIDCacheDisabled
	}
	path := strings.TrimSpace(cfg.DiscIDCache.Path)
	if path == "" {
		return nil, ErrDiscIDCacheNotConfigured
	}
	if logger == nil {
		logger = logging.NewNop()
	}
	return discidcache.NewCache(path, logger), nil
}

// OpenRipCacheManagerForCLI opens the rip cache manager and maps non-fatal config states
// into user-facing warnings suitable for CLI output.
func OpenRipCacheManagerForCLI(req OpenCacheResourceRequest) (*ripcache.Manager, string, error) {
	manager, err := OpenRipCacheManager(req.Config, req.Logger)
	if errors.Is(err, ErrRipCacheDisabled) {
		return nil, "Rip cache is disabled (set rip_cache.enabled = true in config.toml)", nil
	}
	if errors.Is(err, ErrRipCacheDirNotConfigured) {
		return nil, "Rip cache dir is not configured", nil
	}
	if err != nil {
		return nil, "", err
	}
	return manager, "", nil
}

// OpenDiscIDCacheForCLI opens the disc ID cache and maps non-fatal config states
// into user-facing warnings suitable for CLI output.
func OpenDiscIDCacheForCLI(req OpenCacheResourceRequest) (*discidcache.Cache, string, error) {
	cache, err := OpenDiscIDCache(req.Config, req.Logger)
	if errors.Is(err, ErrDiscIDCacheDisabled) {
		return nil, "Disc ID cache is disabled (set disc_id_cache.enabled = true in config.toml)", nil
	}
	if errors.Is(err, ErrDiscIDCacheNotConfigured) {
		return nil, "Disc ID cache path is not configured", nil
	}
	if err != nil {
		return nil, "", err
	}
	return cache, "", nil
}
