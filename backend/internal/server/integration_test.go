package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/auth"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"dnd-bot/backend/migrations"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

type fakeAI struct{ fail bool }

func (f *fakeAI) GenerateTurn(ctx context.Context, model string, in ai.Input) (ai.Output, error) {
	if f.fail {
		return ai.Output{}, errors.New("offline")
	}
	if in.Results != nil {
		return ai.Output{Narrative: "Вы вступили в заброшенную таверну.", Actions: []game.Action{}, Memory: []string{}}, nil
	}
	return ai.Output{Narrative: "Начало", Actions: []game.Action{{Type: "MOVE_SCENE", Name: "Таверна", Description: "Внутри горит камин."}}, Memory: []string{}}, nil
}
func TestIntegration(t *testing.T) {
	database := os.Getenv("TEST_DATABASE_URL")
	if database == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, e := pgxpool.New(ctx, database)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg, e := pgxpool.ParseConfig(database)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	if e = migrations.Run(ctx, pool); e != nil {
		t.Fatal(e)
	}
	if e = migrations.Run(ctx, pool); e != nil {
		t.Fatal("migration not idempotent", e)
	}
	model := &fakeAI{}
	s := &Server{Config: Config{BotToken: "test-bot", SessionSecret: strings.Repeat("s", 32), Model: "test", BotUsername: "test_bot", AppURL: "http://localhost"}, Pool: pool, AI: model}
	s.Hub = NewHub(ctx, s)
	httpServer := httptest.NewServer(s.Router())
	defer httpServer.Close()
	request := func(method, path, token string, body any, status int, dest any) {
		t.Helper()
		req, e := http.NewRequestWithContext(ctx, method, httpServer.URL+path, strings.NewReader(string(blob(body))))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode != status {
			var result any
			json.NewDecoder(res.Body).Decode(&result)
			t.Fatalf("%s %s: got %d want %d: %v", method, path, res.StatusCode, status, result)
		}
		if dest != nil {
			if e = json.NewDecoder(res.Body).Decode(dest); e != nil {
				t.Fatal(e)
			}
		}
	}
	login := func(user int64) string {
		v := url.Values{"auth_date": {fmt.Sprint(time.Now().Unix())}, "user": {fmt.Sprintf(`{"id":%d,"first_name":"Player %d"}`, user, user)}}
		var lines []string
		for k := range v {
			lines = append(lines, k+"="+v.Get(k))
		}
		sort.Strings(lines)
		h := hmac.New(sha256.New, []byte("WebAppData"))
		h.Write([]byte("test-bot"))
		sig := hmac.New(sha256.New, h.Sum(nil))
		sig.Write([]byte(strings.Join(lines, "\n")))
		v.Set("hash", hex.EncodeToString(sig.Sum(nil)))
		var out struct {
			Token string `json:"token"`
		}
		request("POST", "/api/auth/telegram", "", map[string]string{"initData": v.Encode()}, 200, &out)
		return out.Token
	}
	owner, player, outsider := login(1), login(2), login(3)
	request("GET", "/api/me", "invalid", nil, 401, nil)
	var room Room
	request("POST", "/api/rooms", owner, game.Settings{Name: "Test", MaxPlayers: 2}, 201, &room)
	path := "/api/rooms/" + room.ID
	request("GET", path, outsider, nil, 403, nil)
	connect := func(token string) *websocket.Conn {
		conn, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws/rooms/"+room.ID, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = wsjson.Write(ctx, conn, Frame{"auth", map[string]string{"token": token}}); e != nil {
			t.Fatal(e)
		}
		var frame Frame
		if e = wsjson.Read(ctx, conn, &frame); e != nil {
			t.Fatal(e)
		}
		if frame.Type != "room_state" {
			t.Fatal(frame)
		}
		return conn
	}
	ws1 := connect(owner)
	defer ws1.CloseNow()
	request("POST", "/api/rooms/join", player, map[string]string{"code": room.Code}, 200, &room)
	var joined Frame
	if e = wsjson.Read(ctx, ws1, &joined); e != nil || joined.Type != "player_joined" {
		t.Fatal(joined, e)
	}
	ws2 := connect(player)
	defer ws2.CloseNow()
	request("POST", "/api/rooms/join", outsider, map[string]string{"code": room.Code}, 400, nil)
	request("PATCH", path, player, game.Settings{Name: "hacked", MaxPlayers: 2}, 403, nil)
	request("POST", path+"/characters", owner, map[string]string{"name": "Oleg", "race": "Human", "class": "Warrior"}, 200, &room)
	request("POST", path+"/characters", player, map[string]string{"name": "Anna", "race": "Elf", "class": "Mage"}, 200, &room)
	// Drain broadcasts before expecting the result of the next command.
	var f Frame
	for i := 0; i < 2; i++ {
		if e = wsjson.Read(ctx, ws2, &f); e != nil {
			t.Fatal(e)
		}
	}
	if e = wsjson.Write(ctx, ws2, Frame{"ready", map[string]bool{"ready": true}}); e != nil {
		t.Fatal(e)
	}
	for {
		if e = wsjson.Read(ctx, ws2, &f); e != nil {
			t.Fatal(e)
		}
		if f.Type == "room_state" {
			break
		}
	}
	request("GET", path, owner, nil, 200, &room)
	if !room.Members[1].Ready {
		t.Fatal("ready was not persisted")
	}
	var members []Member
	request("GET", path+"/members", owner, nil, 200, &members)
	if len(members) != 2 {
		t.Fatal("member list incorrect")
	}
	characterPath := "/api/characters/" + room.State.Characters[0].ID
	request("PATCH", characterPath, player, map[string]string{"name": "Stolen"}, 403, nil)
	request("PATCH", characterPath, owner, map[string]int{"hp": 999}, 400, nil)
	request("PATCH", characterPath, owner, map[string]string{"name": "Олег"}, 200, &room)
	request("POST", path+"/start", player, nil, 403, nil)
	request("POST", path+"/start", owner, nil, 200, &room)
	cmd := Command{Type: "player_action"}
	cmd.Data.Text = "Входим в таверну"
	if e = s.command(ctx, room.ID, 1, cmd); e != nil {
		t.Fatal(e)
	}
	request("GET", path, owner, nil, 200, &room)
	if room.State.Scene == nil || room.State.Turn != 1 {
		t.Fatal("turn missing")
	}
	before := string(blob(room.State))
	model.fail = true
	if e = s.command(ctx, room.ID, 1, cmd); e == nil {
		t.Fatal("failed AI accepted")
	}
	request("GET", path, owner, nil, 200, &room)
	if string(blob(room.State)) != before {
		t.Fatal("failed AI modified state")
	}
	var events []Event
	request("GET", path+"/events", owner, nil, 200, &events)
	if len(events) != 4 {
		t.Fatalf("unexpected event count: %d", len(events))
	}
	restored := &Server{Config: s.Config, Pool: pool}
	loaded, e := restored.load(ctx, room.ID, 1)
	if e != nil || string(blob(loaded.State)) != before {
		t.Fatal("restore failed", e)
	}
	request("POST", path+"/pause", owner, nil, 200, &room)
	cmd.Type = "roll_dice"
	cmd.Data.Notation = "d20"
	if e = s.command(ctx, room.ID, 1, cmd); e == nil {
		t.Fatal("action accepted while paused")
	}
	model.fail = false
	textOwner := auth.User{ID: 101, FirstName: "Текстовый герой"}
	textPlayer := auth.User{ID: 102, FirstName: "Друг"}
	message, err := s.TextMessage(ctx, textOwner, "/create Чатовая кампания")
	if err != nil || !strings.Contains(message, "создана") {
		t.Fatal("text create failed", message, err)
	}
	selected, err := store.New(pool).GetBotRoom(ctx, textOwner.ID)
	if err != nil {
		t.Fatal(err)
	}
	textRoom, err := s.load(ctx, key(selected), textOwner.ID)
	if err != nil || len(textRoom.State.Characters) != 1 {
		t.Fatal("text hero missing", err)
	}
	message, err = s.TextMessage(ctx, textPlayer, "/join "+textRoom.Code)
	if err != nil || !strings.Contains(message, "Друг") {
		t.Fatal("text join failed", message, err)
	}
	if _, err = s.textMessage(ctx, textPlayer, "/play"); err == nil {
		t.Fatal("text player started owner's campaign")
	}
	message, err = s.TextMessage(ctx, textOwner, "/play")
	if err != nil || !strings.Contains(message, "Игра началась") {
		t.Fatal(message, err)
	}
	message, err = s.TextMessage(ctx, textOwner, "Я вхожу в таверну")
	if err != nil || !strings.Contains(message, "Вы вступили") {
		t.Fatal("text turn failed", message, err)
	}
	message, err = s.TextMessage(ctx, textOwner, "/roll d20")
	if err != nil || !strings.Contains(message, "d20") || strings.Contains(message, "Вы вступили") {
		t.Fatal("text roll repeats old history", message, err)
	}
	message, err = restored.TextMessage(ctx, textOwner, "/state")
	if err != nil || !strings.Contains(message, "Таверна") {
		t.Fatal("text session did not survive server recreation", message, err)
	}
	message, err = s.TextMessage(ctx, textPlayer, "/history")
	if err != nil || !strings.Contains(message, "Вы вступили") {
		t.Fatal("other player cannot read campaign history", message, err)
	}
	t.Log("REST, two WebSockets, permissions, rollback, persistence and full text-bot gameplay verified")
}
