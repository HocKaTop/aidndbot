ALTER TABLE bot_sessions ADD COLUMN notifications BOOLEAN NOT NULL DEFAULT true;
CREATE TABLE telegram_outbox (
 id BIGSERIAL PRIMARY KEY,
 room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 body TEXT NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX telegram_outbox_user_order ON telegram_outbox(user_id,id);
