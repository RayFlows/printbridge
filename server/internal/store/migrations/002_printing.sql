CREATE TABLE print_plans (
    id TEXT PRIMARY KEY,
    owner TEXT NOT NULL,
    payload TEXT NOT NULL
);
CREATE TABLE print_jobs (
    id TEXT PRIMARY KEY,
    owner TEXT NOT NULL,
    request_key TEXT NOT NULL,
    payload TEXT NOT NULL,
    UNIQUE(owner, request_key)
);
CREATE TABLE sessions (
    token_hash BLOB PRIMARY KEY,
    code_hash BLOB NOT NULL,
    expires_at INTEGER NOT NULL
);
