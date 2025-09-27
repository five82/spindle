package main

import (
	"path/filepath"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/encoding"
	"spindle/internal/identification"
	"spindle/internal/organizer"
	"spindle/internal/queue"
	"spindle/internal/ripping"
	"spindle/internal/workflow"
)

type stageRegistrar interface {
	Register(workflow.Stage)
}

func registerStages(reg stageRegistrar, cfg *config.Config, store *queue.Store, logger *zap.Logger) {
	if reg == nil || cfg == nil {
		return
	}

	reg.Register(identification.NewIdentifier(cfg, store, logger))
	reg.Register(ripping.NewRipper(cfg, store, logger))
	reg.Register(encoding.NewEncoder(cfg, store, logger))
	reg.Register(organizer.NewOrganizer(cfg, store, logger))
}

func buildSocketPath(cfg *config.Config) string {
	if cfg == nil {
		return filepath.Join("", "spindle.sock")
	}
	return filepath.Join(cfg.LogDir, "spindle.sock")
}
