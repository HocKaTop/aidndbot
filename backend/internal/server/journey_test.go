package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/auth"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
)

func TestAdventureFromCreationToCompletedGoalAndFinale(t *testing.T) {
	ctx, s, httpServer := reviewServer(t)
	tokens := map[int64]string{}
	for _, user := range []int64{1, 2} {
		if err := store.New(s.Pool).UpsertUser(ctx, store.UpsertUserParams{ID: user, FirstName: fmt.Sprint("Player", user)}); err != nil {
			t.Fatal(err)
		}
		tokens[user] = auth.Issue(auth.User{ID: user}, s.Config.SessionSecret, time.Now())
	}
	request := func(user int64, method, path string, body any) Room {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, httpServer.URL+"/api"+path, strings.NewReader(string(blob(body))))
		req.Header.Set("Authorization", "Bearer "+tokens[user])
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode >= 300 {
			t.Fatalf("%s %s: %d", method, path, res.StatusCode)
		}
		var room Room
		if err = json.NewDecoder(res.Body).Decode(&room); err != nil {
			t.Fatal(err)
		}
		return room
	}
	r := request(1, "POST", "/rooms", game.Settings{Name: "Последний фонарь", MaxPlayers: 2})
	path := "/rooms/" + r.ID
	r = request(2, "POST", "/rooms/join", map[string]string{"code": r.Code})
	for _, user := range []int64{1, 2} {
		r = request(user, "POST", path+"/characters", map[string]string{"name": fmt.Sprint("Hero", user), "race": "Человек", "class": "Воин"})
	}
	sockets := map[int64]*websocket.Conn{}
	last := map[int64]time.Time{}
	for _, user := range []int64{1, 2} {
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		sockets[user] = conn
		if err = wsjson.Write(ctx, conn, Frame{"auth", map[string]string{"token": tokens[user]}}); err != nil {
			t.Fatal(err)
		}
	}
	command := func(user int64, cmd Command, want string) {
		t.Helper()
		if wait := 1100*time.Millisecond - time.Since(last[user]); wait > 0 {
			time.Sleep(wait)
		}
		last[user] = time.Now()
		cmd.ID = uuid.NewString()
		if err := wsjson.Write(ctx, sockets[user], cmd); err != nil {
			t.Fatal(err)
		}
		for {
			var frame struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			}
			if err := wsjson.Read(ctx, sockets[user], &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Type != "command_status" {
				continue
			}
			var status commandStatus
			if err := json.Unmarshal(frame.Data, &status); err != nil {
				t.Fatal(err)
			}
			if status.ID != cmd.ID || status.Status == "queued" || status.Status == "processing" {
				continue
			}
			if status.Status != want {
				t.Fatalf("%s: %+v", cmd.Type, status)
			}
			break
		}
		r = request(user, "GET", path, nil)
	}
	command(1, Command{Type: "start_game"}, "failed")
	for _, user := range []int64{1, 2} {
		cmd := Command{Type: "ready"}
		cmd.Data.Ready = true
		command(user, cmd, "completed")
	}
	s.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		if in.Opening {
			return openingOutput(), nil
		}
		if in.Results != nil {
			return ai.Output{Narrative: "Ваши действия изменили историю.", Actions: []game.Action{}}, nil
		}
		out := ai.Output{Narrative: "Продолжаем приключение."}
		switch in.Text {
		case "Идём к мельнице":
			out.Actions = []game.Action{{Type: "MOVE_SCENE", Name: "Мельница", Description: "Внутри прячется похититель."}, {Type: "CREATE_NPC", Name: "Похититель", Status: "neutral"}}
		case "Договариваюсь и забираю камень":
			out.Actions = []game.Action{{Type: "SET_DISPOSITION", Target: in.State.NPCs[0].ID, Status: "friendly"}, {Type: "ADD_ITEM", Name: "Огненный камень"}}
		case "Возвращаемся к мосту":
			for _, place := range in.State.Locations {
				if place.Title == "Мост" {
					out.Actions = []game.Action{{Type: "REVISIT_SCENE", Target: place.ID}}
					break
				}
			}
			if len(out.Actions) == 0 {
				return ai.Output{}, fmt.Errorf("known bridge was not saved")
			}
		case "Устанавливаю камень в фонарь":
			for _, item := range in.State.Hero(in.PlayerID).Inventory {
				if item.Name == "Огненный камень" {
					out.Actions = append(out.Actions, game.Action{Type: "REMOVE_ITEM", Target: item.ID})
				}
			}
			if len(out.Actions) != 1 {
				return ai.Output{}, fmt.Errorf("acting player lacks quest item")
			}
			out.Actions = append(out.Actions, game.Action{Type: "PROPOSE_QUEST_COMPLETION", Target: in.State.Quests[0].ID, Description: "Огненный камень установлен в фонарь."})
		default:
			return ai.Output{}, fmt.Errorf("unexpected scenario action")
		}
		return out, nil
	})
	command(1, Command{Type: "start_game"}, "completed")
	if r.Status != "PLAYING" || r.State.Scene == nil || len(r.State.Quests) != 1 {
		t.Fatal("opening missing")
	}
	for _, step := range []struct {
		user int64
		text string
	}{{1, "Идём к мельнице"}, {2, "Договариваюсь и забираю камень"}, {1, "Возвращаемся к мосту"}, {2, "Устанавливаю камень в фонарь"}} {
		cmd := Command{Type: "player_action", ExpectedTurn: &r.State.Turn}
		cmd.Data.Text = step.text
		command(step.user, cmd, "completed")
	}
	if r.State.Quests[0].Status != "ACTIVE" || r.State.PendingQuestCompletion == nil || r.State.Scene.Location != "Мост" || r.State.Turn != 4 || r.State.Combat {
		t.Fatal("goal was not proposed", r.State)
	}
	for _, h := range r.State.Characters {
		for _, item := range h.Inventory {
			if item.Name == "Огненный камень" {
				t.Fatal("quest item not consumed")
			}
		}
	}
	finish := Command{Type: "confirm_quest", ExpectedTurn: &r.State.Turn}
	finish.Data.ProposalID = r.State.PendingQuestCompletion.ID
	command(2, finish, "failed")
	command(1, finish, "completed")
	if r.Status != "FINISHED" || r.State.Ending == nil {
		t.Fatal("finale not saved")
	}
	command(2, Command{Type: "start_game"}, "failed")
	restored := &Server{Config: s.Config, Pool: s.Pool}
	loaded, err := restored.load(ctx, r.ID, 2)
	if err != nil || loaded.State.Ending.Reason != "objective" || loaded.State.Quests[0].Status != "COMPLETED" {
		t.Fatal("finale lost on reload", err)
	}
	events, err := s.eventList(ctx, r.ID)
	if err != nil || events[0].Type != "GAME_STARTED" || events[len(events)-1].Type != "GAME_FINISHED" {
		t.Fatal("incomplete campaign history", err)
	}
	t.Log("Two players: create → join → heroes → ready → opening → travel → peaceful encounter → objective proposal → owner confirms finale → reload")
}
