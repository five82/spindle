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
