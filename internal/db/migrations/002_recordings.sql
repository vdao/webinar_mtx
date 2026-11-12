ALTER TABLE webinars
    ADD COLUMN record_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN recording_id TEXT UNIQUE,
    ADD COLUMN recording_status TEXT NOT NULL DEFAULT 'none',
    ADD COLUMN recording_urls TEXT NOT NULL DEFAULT '';
