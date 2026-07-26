CREATE TABLE IF NOT EXISTS session_metadata (
    session_id TEXT PRIMARY KEY NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    title BLOB,
    archived_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_session_metadata_archived
    ON session_metadata(archived_at);

ALTER TABLE messages ADD COLUMN turn_id TEXT;

CREATE INDEX IF NOT EXISTS idx_messages_turn
    ON messages(turn_id, created_at);
