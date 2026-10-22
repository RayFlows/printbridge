CREATE TABLE users (
    id TEXT PRIMARY KEY,
    app_id TEXT NOT NULL,
    open_id TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    name_prompted INTEGER NOT NULL DEFAULT 0,
    banned INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    UNIQUE(app_id, open_id)
);
ALTER TABLE sessions ADD COLUMN user_id TEXT NOT NULL DEFAULT '';
CREATE TABLE account_settings (
    id INTEGER PRIMARY KEY CHECK (id=1),
    printing_enabled INTEGER NOT NULL CHECK (printing_enabled IN (0,1))
);
INSERT INTO account_settings(id, printing_enabled) VALUES(1,1);
