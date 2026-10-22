CREATE TABLE documents (
    id TEXT PRIMARY KEY,
    owner TEXT NOT NULL,
    name TEXT NOT NULL,
    bytes INTEGER NOT NULL CHECK (bytes > 0),
    page_count INTEGER NOT NULL CHECK (page_count BETWEEN 1 AND 500),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX documents_expiry ON documents(expires_at);
