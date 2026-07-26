CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY NOT NULL,
    parent_id TEXT REFERENCES sessions(id),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    context_tokens INTEGER NOT NULL DEFAULT 0 CHECK (context_tokens >= 0)
);

CREATE INDEX IF NOT EXISTS idx_sessions_parent_id ON sessions(parent_id);
CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at);

CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY NOT NULL,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    memory_type TEXT NOT NULL DEFAULT 'transcript',
    content BLOB NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_session_created
    ON messages(session_id, created_at);

CREATE VIRTUAL TABLE IF NOT EXISTS message_metadata_fts USING fts5(
    message_id UNINDEXED,
    session_id,
    role,
    memory_type,
    created_at,
    tokenize = 'unicode61'
);

CREATE TRIGGER IF NOT EXISTS messages_fts_insert
AFTER INSERT ON messages BEGIN
    INSERT INTO message_metadata_fts(
        message_id, session_id, role, memory_type, created_at
    ) VALUES (
        new.id, new.session_id, new.role, new.memory_type, new.created_at
    );
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_delete
AFTER DELETE ON messages BEGIN
    DELETE FROM message_metadata_fts WHERE message_id = old.id;
END;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY NOT NULL,
    applied_at INTEGER NOT NULL
);
