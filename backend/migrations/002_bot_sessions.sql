CREATE TABLE bot_sessions (
 user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE
);
CREATE INDEX bot_sessions_room_idx ON bot_sessions(room_id);
