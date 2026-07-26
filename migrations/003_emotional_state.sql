CREATE TABLE IF NOT EXISTS emotional_state (
    user_id TEXT PRIMARY KEY NOT NULL,
    state BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);
