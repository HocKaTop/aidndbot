package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/auth"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"dnd-bot/backend/migrations"
	"errors"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type plannedAI struct {
	actions       []game.Action
	failNarration bool
}

func TestCombatWithOnlyRemoteEnemiesEnds(t *testing.T) {
	r := Room{Status: "PLAYING", State: game.NewState(game.Settings{})}
	r.State.Characters = []game.Character{game.NewCharacter(1, "Hero", "Human", "Warrior")}
	r.State.Scene = &game.Scene{Location: "Tavern"}
	r.State.NPCs = []game.NPC{{Name: "Remote enemy", HP: 12, Alive: true, Disposition: "hostile", Location: "Forest"}}
	r.State.Combat = true
	results, err := settleTurn(&r, 1, true, nil)
	if err != nil || r.State.Combat || len(results) != 1 || results[0].Type != "COMBAT_ENDED" || r.State.Characters[0].HP != 20 {
		t.Fatal("remote enemy kept combat active", results, err)
	}
}

func (p *plannedAI) GenerateTurn(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
	if in.Opening {
		return openingOutput(), nil
	}
	if in.Results != nil && p.failNarration {
		return ai.Output{}, errors.New("narration unavailable")
	}
	if in.Results != nil {
		return ai.Output{Narrative: "Результаты учтены.", Actions: []game.Action{}, Memory: []string{}}, nil
	}
	return ai.Output{Narrative: "Ход.", Actions: p.actions, Memory: []string{}}, nil
}
func reviewServer(t *testing.T) (context.Context, *Server, *httptest.Server) {
	t.Helper()
	database := os.Getenv("TEST_DATABASE_URL")
	if database == "" {
		t.Skip("set TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	admin, e := pgxpool.New(ctx, database)
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	schema := "review_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		admin.Close()
		cancel()
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(database)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		cancel()
		pool.Close()
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	if e = migrations.Run(ctx, pool); e != nil {
		t.Fatal(e)
	}
	s := &Server{Config: Config{BotToken: "test", SessionSecret: strings.Repeat("s", 32), Model: "test", AppURL: "http://localhost"}, Pool: pool, AI: &plannedAI{}}
	s.Hub = NewHub(ctx, s)
	httpServer := httptest.NewServer(s.Router())
	t.Cleanup(httpServer.Close)
	return ctx, s, httpServer
}
func testCampaign(t *testing.T, ctx context.Context, s *Server, user int64) Room {
	t.Helper()
	q := store.New(s.Pool)
	if e := q.UpsertUser(ctx, store.UpsertUserParams{ID: user, FirstName: "Tester"}); e != nil {
		t.Fatal(e)
	}
	hero := game.NewCharacter(user, "Tester", "Human", "Warrior")
	r, e := s.createCampaign(ctx, user, game.Settings{Name: "Review", MaxPlayers: 2}, &hero)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestDeletionFlow(t *testing.T) {
	ctx, s, httpServer := reviewServer(t)
	room := testCampaign(t, ctx, s, 1)
	q := store.New(s.Pool)
	rid, _ := id(room.ID)
	q.UpsertUser(ctx, store.UpsertUserParams{ID: 2, FirstName: "Player"})
	_, e := s.mutate(ctx, room.ID, 1, false, func(q *store.Queries, r *Room) error {
		r.State.NPCs = []game.NPC{{ID: uuid.NewString(), Name: "NPC", HP: 12, MaxHP: 12, Alive: true}}
		r.State.Quests = []game.Quest{{ID: uuid.NewString(), Title: "Quest", Status: "ACTIVE"}}
		r.State.Scene = &game.Scene{ID: uuid.NewString(), Title: "Scene"}
		if e := s.addEvent(ctx, q, r.ID, 1, "TEST", game.Result{Text: "history"}); e != nil {
			return e
		}
		return q.AddMember(ctx, store.AddMemberParams{RoomID: rid, UserID: 2, Role: "PLAYER"})
	})
	if e != nil {
		t.Fatal(e)
	}
	request := func(user int64, code string, want int) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "DELETE", httpServer.URL+"/api/rooms/"+room.ID, strings.NewReader(string(blob(map[string]string{"code": code}))))
		if user > 0 {
			req.Header.Set("Authorization", "Bearer "+auth.Issue(auth.User{ID: user}, s.Config.SessionSecret, time.Now()))
		}
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("delete user %d: got %d want %d", user, res.StatusCode, want)
		}
	}
	sockets := []*websocket.Conn{}
	for _, user := range []int64{1, 2} {
		conn, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws/rooms/"+room.ID, nil)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { conn.CloseNow() })
		if e = wsjson.Write(ctx, conn, Frame{"auth", map[string]string{"token": auth.Issue(auth.User{ID: user}, s.Config.SessionSecret, time.Now())}}); e != nil {
			t.Fatal(e)
		}
		var frame Frame
		if e = wsjson.Read(ctx, conn, &frame); e != nil || frame.Type != "room_state" {
			t.Fatal(frame, e)
		}
		sockets = append(sockets, conn)
	}
	request(0, room.Code, 401)
	request(2, room.Code, 403)
	request(3, room.Code, 403)
	request(1, "wrong", 400)
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.New(tx).LockRoom(ctx, rid); e != nil {
		t.Fatal(e)
	}
	request(1, room.Code, 409)
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	request(1, room.Code, 200)
	for _, conn := range sockets {
		var frame Frame
		if e = wsjson.Read(ctx, conn, &frame); e != nil || frame.Type != "room_deleted" {
			t.Fatal("terminal event missing", frame, e)
		}
		if e = wsjson.Read(ctx, conn, &frame); websocket.CloseStatus(e) != websocket.StatusNormalClosure {
			t.Fatal("socket not closed", e)
		}
	}
	if _, e = s.load(ctx, room.ID, 1); e == nil {
		t.Fatal("deleted room still readable")
	}
	if _, e = q.FindRoom(ctx, room.Code); e == nil {
		t.Fatal("invite still usable")
	}
	for _, table := range []string{"room_members", "characters", "npcs", "quests", "scenes", "game_events", "campaign_summaries", "bot_sessions"} {
		var count int
		if e = s.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE room_id=$1", rid).Scan(&count); e != nil || count != 0 {
			t.Fatal("cascade failed", table, count, e)
		}
	}
	var count int
	if e = s.Pool.QueryRow(ctx, "SELECT count(*) FROM inventory_items").Scan(&count); e != nil || count != 0 {
		t.Fatal("orphaned inventory", count, e)
	}
	if e = s.Pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&count); e != nil || count != 2 {
		t.Fatal("users were deleted", count, e)
	}
	s.Hub.mu.Lock()
	_, exists := s.Hub.rooms[room.ID]
	s.Hub.mu.Unlock()
	if exists {
		t.Fatal("runtime retained")
	}
	request(1, room.Code, 404)
	t.Log("owner-only deletion, confirmation, busy conflict, two clients, cascade, invite and runtime cleanup verified")
}
func TestTextDeletion(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	room := testCampaign(t, ctx, s, 1)
	u := auth.User{ID: 1, FirstName: "Tester"}
	text, e := s.TextMessage(ctx, u, "/delete")
	if e != nil || !strings.Contains(text, "/delete "+room.Code) {
		t.Fatal(text, e)
	}
	if _, e = s.load(ctx, room.ID, 1); e != nil {
		t.Fatal("prompt deleted room", e)
	}
	text, e = s.TextMessage(ctx, u, "/delete wrong")
	if e != nil || !strings.Contains(text, "код комнаты") {
		t.Fatal(text, e)
	}
	text, e = s.TextMessage(ctx, u, "/delete "+room.Code)
	if e != nil || !strings.Contains(text, "Комната удалена") {
		t.Fatal(text, e)
	}
	if _, e = store.New(s.Pool).GetBotRoom(ctx, 1); e == nil {
		t.Fatal("selection retained")
	}
}
func TestTurnRulesAndRollback(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	room := testCampaign(t, ctx, s, 1)
	model := s.AI.(*plannedAI)
	ready := Command{Type: "ready"}
	ready.Data.Ready = true
	if e := s.command(ctx, room.ID, 1, ready); e != nil {
		t.Fatal(e)
	}
	if e := s.startGame(ctx, room.ID, 1); e != nil {
		t.Fatal(e)
	}
	// A failed check may request a reward, but the engine must not grant it.
	model.actions = []game.Action{{Type: "SKILL_CHECK", Skill: "strength", DC: 25}, {Type: "ADD_ITEM", Name: "Treasure"}}
	cmd := Command{Type: "player_action"}
	cmd.Data.Text = "Открываю сундук"
	if e := s.command(ctx, room.ID, 1, cmd); e != nil {
		t.Fatal(e)
	}
	saved, e := s.load(ctx, room.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	if len(saved.State.Characters[0].Inventory) != 2 {
		t.Fatal("failed check granted item")
	}
	events, e := s.eventList(ctx, room.ID)
	if e != nil {
		t.Fatal(e)
	}
	expected := []string{"GAME_STARTED", "MOVE_SCENE", "CREATE_QUEST", "GM_MESSAGE", "PLAYER_ACTION", "SKILL_CHECK", "ACTION_SKIPPED", "GM_MESSAGE"}
	for i, kind := range expected {
		if len(events) <= i || events[i].Type != kind {
			t.Fatal("event order incorrect", events)
		}
	}
	before := string(blob(saved.State))
	eventCount := len(events)
	cmd.Data.Text = "Иду в соседнюю комнату"
	for _, failNarration := range []bool{false, true} {
		model.actions = []game.Action{{Type: "MOVE_SCENE", Name: "Changed", Description: "New scene"}}
		model.failNarration = failNarration
		if !failNarration {
			model.actions = append(model.actions, game.Action{Type: "UNKNOWN"})
		}
		if e = s.command(ctx, room.ID, 1, cmd); e == nil {
			t.Fatal("invalid turn succeeded")
		}
		current, e := s.load(ctx, room.ID, 1)
		if e != nil || string(blob(current.State)) != before {
			t.Fatal("partial state committed", e)
		}
		events, e = s.eventList(ctx, room.ID)
		if e != nil || len(events) != eventCount {
			t.Fatal("partial history committed", e)
		}
	}
	// Consuming a potion in combat must cost a turn and allow an enemy response.
	_, e = s.mutate(ctx, room.ID, 1, false, func(q *store.Queries, r *Room) error {
		r.State.Characters[0].HP = 10
		r.State.Combat = true
		r.State.NPCs = []game.NPC{{ID: uuid.NewString(), Name: "Goblin", HP: 12, MaxHP: 12, Alive: true, Disposition: "hostile", Location: r.State.Scene.Location}}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	cmd.Type = "use_item"
	cmd.Data.ItemID = room.State.Characters[0].Inventory[0].ID
	if e = s.command(ctx, room.ID, 1, cmd); e != nil {
		t.Fatal(e)
	}
	current, e := s.load(ctx, room.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	if current.State.Turn != 2 || current.State.Characters[0].Inventory[0].Quantity != 1 {
		t.Fatal("potion did not consume turn/item")
	}
	events, e = s.eventList(ctx, room.ID)
	if e != nil {
		t.Fatal(e)
	}
	if events[len(events)-3].Type != "ITEM_USED" || events[len(events)-2].Type != "NPC_ATTACK" || events[len(events)-1].Type != "TURN_CHANGED" {
		t.Fatal("potion bypassed retaliation", events)
	}
	// The state transition is deterministic even if the last attack's roll is random.
	current.State.Characters[0].HP = 0
	results, e := settleTurn(&current, 1, true, nil)
	if e != nil || current.Status != "FINISHED" || current.State.Combat || results[len(results)-1].Type != "GAME_FINISHED" {
		t.Fatal("defeat not finalized", e)
	}
}
