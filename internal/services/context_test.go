package services_test

import (
	"context"
	"testing"

	"spindle/internal/services"
)

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()
	ctx = services.WithItemID(ctx, 42)
	ctx = services.WithStage(ctx, "encoding")
	ctx = services.WithRequestID(ctx, "req-123")

	if id, ok := services.ItemIDFromContext(ctx); !ok || id != 42 {
		t.Fatalf("unexpected item id: %v %v", id, ok)
	}
	if stage, ok := services.StageFromContext(ctx); !ok || stage != "encoding" {
		t.Fatalf("unexpected stage: %v %v", stage, ok)
	}
	if rid, ok := services.RequestIDFromContext(ctx); !ok || rid != "req-123" {
		t.Fatalf("unexpected request id: %v %v", rid, ok)
	}
}

func TestStageBlankPreservesContext(t *testing.T) {
	ctx := context.Background()
	ctx = services.WithStage(ctx, "")
	if _, ok := services.StageFromContext(ctx); ok {
		t.Fatal("expected no stage value")
	}
}
