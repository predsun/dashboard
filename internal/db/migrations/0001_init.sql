-- 0001_init.sql — initial schema.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE TABLE categories (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE apps (
    id                    INTEGER PRIMARY KEY,
    name                  TEXT    NOT NULL,
    url                   TEXT    NOT NULL,
    description           TEXT    NOT NULL DEFAULT '',
    icon_path             TEXT    NOT NULL DEFAULT '',
    category_id           INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    sort_order            INTEGER NOT NULL DEFAULT 0,
    health_check_enabled  INTEGER NOT NULL DEFAULT 0,
    health_check_url      TEXT    NOT NULL DEFAULT '',
    created_at            INTEGER NOT NULL,
    updated_at            INTEGER NOT NULL
);

CREATE INDEX idx_apps_sort     ON apps(sort_order);
CREATE INDEX idx_apps_category ON apps(category_id);

CREATE TABLE health_status (
    app_id               INTEGER PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    status               TEXT    NOT NULL DEFAULT 'unknown',  -- up | down | unknown
    last_checked_at      INTEGER,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    next_check_at        INTEGER
);

CREATE INDEX idx_health_next_check ON health_status(next_check_at);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE sessions (
    id         TEXT    PRIMARY KEY,                                       -- 32-byte hex
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_sessions_expires ON sessions(expires_at);
