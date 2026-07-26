CREATE TABLE IF NOT EXISTS living_state (
    key_id TEXT PRIMARY KEY NOT NULL,
    kind TEXT NOT NULL,
    scope TEXT NOT NULL,
    state BLOB NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(kind, scope)
);

CREATE INDEX IF NOT EXISTS idx_living_state_kind_scope
    ON living_state(kind, scope);
