CREATE TABLE IF NOT EXISTS webinars (
    id           TEXT PRIMARY KEY,
    slug         TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    stream_key   TEXT NOT NULL UNIQUE,
    status       TEXT NOT NULL DEFAULT 'draft',
    scheduled_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS participants (
    id          TEXT PRIMARY KEY,
    webinar_id  TEXT NOT NULL REFERENCES webinars(id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'active',
    source      TEXT NOT NULL DEFAULT 'manual',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ,
    UNIQUE (webinar_id, email)
);

CREATE INDEX IF NOT EXISTS idx_participants_webinar ON participants(webinar_id);

CREATE TABLE IF NOT EXISTS sessions (
    id             TEXT PRIMARY KEY,
    participant_id TEXT NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
    webinar_id     TEXT NOT NULL REFERENCES webinars(id) ON DELETE CASCADE,
    token_hash     TEXT NOT NULL UNIQUE,
    expires_at     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_sessions_expiry ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS login_codes (
    id         TEXT PRIMARY KEY,
    webinar_id TEXT NOT NULL REFERENCES webinars(id) ON DELETE CASCADE,
    email      TEXT NOT NULL,
    code_hash  TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    attempts   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_login_codes_lookup ON login_codes(webinar_id, email);

CREATE TABLE IF NOT EXISTS webhooks (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    secret             TEXT NOT NULL,
    email_path         TEXT NOT NULL DEFAULT '',
    product_path       TEXT NOT NULL DEFAULT '',
    default_webinar_id TEXT REFERENCES webinars(id) ON DELETE SET NULL,
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS webhook_products (
    webhook_id  TEXT NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    external_id TEXT NOT NULL,
    webinar_id  TEXT NOT NULL REFERENCES webinars(id) ON DELETE CASCADE,
    PRIMARY KEY (webhook_id, external_id)
);
