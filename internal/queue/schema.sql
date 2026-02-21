-- Spindle queue schema (version 4)
-- This is a transient database for tracking in-flight jobs.
-- On schema changes, bump schemaVersion in schema.go and clear the database.

CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS queue_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_path TEXT,
    disc_title TEXT,
    status TEXT NOT NULL,
    failed_at_status TEXT,
    media_info_json TEXT,
    ripped_file TEXT,
    encoded_file TEXT,
    final_file TEXT,
    error_message TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    progress_stage TEXT,
    progress_percent REAL DEFAULT 0.0,
    progress_message TEXT,
    rip_spec_data TEXT,
    disc_fingerprint TEXT,
    metadata_json TEXT,
    last_heartbeat TIMESTAMP,
    needs_review INTEGER NOT NULL DEFAULT 0,
    review_reason TEXT,
    item_log_path TEXT,
    encoding_details_json TEXT,
    active_episode_key TEXT,
    progress_bytes_copied INTEGER DEFAULT 0,
    progress_total_bytes INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_queue_status ON queue_items(status);
CREATE INDEX IF NOT EXISTS idx_queue_fingerprint ON queue_items(disc_fingerprint);
