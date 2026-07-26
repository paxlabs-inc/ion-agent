CREATE TABLE IF NOT EXISTS cognition_state (
    session_id TEXT PRIMARY KEY NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    state BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS turn_state (
    turn_id TEXT PRIMARY KEY NOT NULL,
    actor_id TEXT NOT NULL,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    state BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_turn_state_session_updated
    ON turn_state(session_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_turn_state_recovery
    ON turn_state(status, updated_at);
