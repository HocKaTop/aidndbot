CREATE TABLE users (
 id BIGINT PRIMARY KEY, first_name TEXT NOT NULL, username TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE rooms (
 id UUID PRIMARY KEY, code TEXT UNIQUE NOT NULL, owner_id BIGINT NOT NULL REFERENCES users(id),
 name TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'WAITING' CHECK(status IN ('WAITING','PLAYING','PAUSED','FINISHED')),
 state JSONB NOT NULL DEFAULT '{}', created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX rooms_owner_idx ON rooms(owner_id);
CREATE TABLE room_members (
 room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, user_id BIGINT NOT NULL REFERENCES users(id),
 role TEXT NOT NULL CHECK(role IN ('OWNER','PLAYER')), ready BOOLEAN NOT NULL DEFAULT false, joined_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(room_id,user_id)
);
CREATE INDEX members_user_idx ON room_members(user_id);
CREATE TABLE characters (
 id UUID PRIMARY KEY, room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, user_id BIGINT NOT NULL REFERENCES users(id),
 data JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(room_id,user_id)
);
CREATE TABLE npcs (
 id UUID PRIMARY KEY, room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, data JSONB NOT NULL
);
CREATE INDEX npcs_room_idx ON npcs(room_id);
CREATE TABLE inventory_items (
 id UUID PRIMARY KEY, character_id UUID NOT NULL REFERENCES characters(id) ON DELETE CASCADE, data JSONB NOT NULL
);
CREATE INDEX inventory_character_idx ON inventory_items(character_id);
CREATE TABLE quests (
 id UUID PRIMARY KEY, room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, data JSONB NOT NULL
);
CREATE INDEX quests_room_idx ON quests(room_id);
CREATE TABLE scenes (
 id UUID PRIMARY KEY, room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, data JSONB NOT NULL
);
CREATE INDEX scenes_room_idx ON scenes(room_id);
CREATE TABLE game_events (
 id UUID PRIMARY KEY, room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, actor_id BIGINT REFERENCES users(id),
 type TEXT NOT NULL, payload JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX events_room_time_idx ON game_events(room_id,created_at);
CREATE TABLE campaign_summaries (
 room_id UUID PRIMARY KEY REFERENCES rooms(id) ON DELETE CASCADE, summary TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
