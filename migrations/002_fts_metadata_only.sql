DROP TRIGGER IF EXISTS messages_fts_insert;
DROP TRIGGER IF EXISTS messages_fts_delete;
DROP TABLE IF EXISTS message_metadata_fts;

CREATE VIRTUAL TABLE message_metadata_fts USING fts5(
    message_id,
    session_id,
    memory_type,
    created_at,
    tokenize = 'unicode61'
);

CREATE TRIGGER messages_fts_insert
AFTER INSERT ON messages BEGIN
    INSERT INTO message_metadata_fts(
        message_id, session_id, memory_type, created_at
    ) VALUES (
        new.id, new.session_id, new.memory_type, new.created_at
    );
END;

CREATE TRIGGER messages_fts_delete
AFTER DELETE ON messages BEGIN
    DELETE FROM message_metadata_fts WHERE message_id = old.id;
END;
