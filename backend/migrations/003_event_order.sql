-- now() is constant within a transaction. UUID order cannot represent action order.
-- Historical ties retain their existing UUID order; new events get insertion order.
ALTER TABLE game_events ADD COLUMN sequence BIGINT;
WITH ordered AS (
 SELECT id, row_number() OVER (ORDER BY created_at,id) AS ordinal FROM game_events
)
UPDATE game_events e SET sequence=o.ordinal FROM ordered o WHERE e.id=o.id;
CREATE SEQUENCE game_event_sequence OWNED BY game_events.sequence;
SELECT setval('game_event_sequence', COALESCE((SELECT max(sequence) FROM game_events),0)+1, false);
ALTER TABLE game_events ALTER COLUMN sequence SET DEFAULT nextval('game_event_sequence');
ALTER TABLE game_events ALTER COLUMN sequence SET NOT NULL;
CREATE UNIQUE INDEX events_sequence_idx ON game_events(sequence);
CREATE INDEX events_room_sequence_idx ON game_events(room_id,sequence);
