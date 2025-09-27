ALTER TABLE queue_items ADD COLUMN needs_review INTEGER NOT NULL DEFAULT 0;
ALTER TABLE queue_items ADD COLUMN review_reason TEXT;
