CREATE TABLE command_receipts (
 room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 command_id UUID NOT NULL,
 request_hash TEXT NOT NULL,
 events JSONB NOT NULL,
 completed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(room_id,user_id,command_id)
);
