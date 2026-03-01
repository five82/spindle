package api

import (
	"context"
	"fmt"

	"spindle/internal/auditgather"
	"spindle/internal/config"
	"spindle/internal/queue"
)

type GatherAuditReportRequest struct {
	Config *config.Config
	ItemID int64
}

func GatherAuditReport(ctx context.Context, req GatherAuditReportRequest) (*auditgather.Report, error) {
	cfg := req.Config
	if cfg == nil {
		return nil, fmt.Errorf("configuration is required")
	}
	if req.ItemID <= 0 {
		return nil, fmt.Errorf("invalid item id %d", req.ItemID)
	}

	store, err := queue.Open(cfg)
	if err != nil {
		return nil, fmt.Errorf("open queue store: %w", err)
	}
	defer store.Close()

	item, err := store.GetByID(ctx, req.ItemID)
	if err != nil {
		return nil, fmt.Errorf("fetch item: %w", err)
	}
	if item == nil {
		return nil, fmt.Errorf("queue item %d not found", req.ItemID)
	}

	report, err := auditgather.Gather(ctx, cfg, item)
	if err != nil {
		return nil, fmt.Errorf("gather audit data: %w", err)
	}
	return report, nil
}
