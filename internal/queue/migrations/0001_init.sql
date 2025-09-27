CREATE TABLE IF NOT EXISTS queue_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_path TEXT,
    disc_title TEXT,
    status TEXT NOT NULL,
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
    last_heartbeat TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_queue_status ON queue_items(status);
CREATE INDEX IF NOT EXISTS idx_queue_fingerprint ON queue_items(disc_fingerprint);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY
);
