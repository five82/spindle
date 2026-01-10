-- Add byte progress tracking for organizing stage
ALTER TABLE queue_items ADD COLUMN progress_bytes_copied INTEGER DEFAULT 0;
ALTER TABLE queue_items ADD COLUMN progress_total_bytes INTEGER DEFAULT 0;
