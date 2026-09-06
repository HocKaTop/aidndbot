-- name: UpsertUser :exec
INSERT INTO users(id,first_name,username) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET first_name=EXCLUDED.first_name,username=EXCLUDED.username;
-- name: CreateRoom :exec
INSERT INTO rooms(id,code,owner_id,name,state) VALUES($1,$2,$3,$4,$5);
-- name: GetRoom :one
SELECT * FROM rooms WHERE id=$1;
-- name: LockRoom :one
SELECT * FROM rooms WHERE id=$1 FOR UPDATE;
-- name: FindRoom :one
SELECT * FROM rooms WHERE code=$1;
-- name: ListRooms :many
SELECT r.* FROM rooms r JOIN room_members m ON m.room_id=r.id WHERE m.user_id=$1 ORDER BY r.created_at DESC;
-- name: SaveRoom :exec
UPDATE rooms SET name=$2,status=$3,state=$4,updated_at=now() WHERE id=$1;
-- name: AddMember :exec
INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT DO NOTHING;
-- name: RemoveMember :exec
DELETE FROM room_members WHERE room_id=$1 AND user_id=$2;
-- name: ListMembers :many
SELECT m.*,u.first_name,u.username FROM room_members m JOIN users u ON u.id=m.user_id WHERE room_id=$1 ORDER BY joined_at;
-- name: SetReady :exec
UPDATE room_members SET ready=$3 WHERE room_id=$1 AND user_id=$2;
-- name: AddEvent :exec
INSERT INTO game_events(id,room_id,actor_id,type,payload) VALUES($1,$2,$3,$4,$5);
-- name: ListEvents :many
SELECT * FROM (SELECT * FROM game_events WHERE room_id=$1 ORDER BY sequence DESC LIMIT 100) recent ORDER BY sequence;
-- name: SaveCharacter :exec
INSERT INTO characters(id,room_id,user_id,data) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET data=EXCLUDED.data;
-- name: DeleteCharacter :exec
DELETE FROM characters WHERE room_id=$1 AND user_id=$2;
-- name: SaveNPC :exec
INSERT INTO npcs(id,room_id,data) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET data=EXCLUDED.data;
-- name: SaveQuest :exec
INSERT INTO quests(id,room_id,data) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET data=EXCLUDED.data;
-- name: SaveScene :exec
INSERT INTO scenes(id,room_id,data) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET data=EXCLUDED.data;
-- name: ClearInventory :exec
DELETE FROM inventory_items WHERE character_id=$1;
-- name: SaveItem :exec
INSERT INTO inventory_items(id,character_id,data) VALUES($1,$2,$3);
-- name: SaveSummary :exec
INSERT INTO campaign_summaries(room_id,summary) VALUES($1,$2) ON CONFLICT(room_id) DO UPDATE SET summary=EXCLUDED.summary,updated_at=now();
-- name: GetCharacter :one
SELECT * FROM characters WHERE id=$1;
-- name: SelectBotRoom :exec
INSERT INTO bot_sessions(user_id,room_id) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET room_id=EXCLUDED.room_id;
-- name: GetBotRoom :one
SELECT room_id FROM bot_sessions WHERE user_id=$1;

-- name: LockRoomNowait :one
SELECT * FROM rooms WHERE id=$1 FOR UPDATE NOWAIT;
-- name: DeleteRoom :exec
DELETE FROM rooms WHERE id=$1;
