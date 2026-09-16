package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/auth"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"errors"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestCommandReceiptSurvivesRestartAndPreventsDuplicateTurn(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	calls := 0
	s.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		calls++
		return ai.Output{Narrative: "Ты осмотрел мост.", Actions: []game.Action{}}, nil
	})
	version := 0
	cmd := Command{ID: uuid.NewString(), Type: "player_action", ExpectedTurn: &version}
	cmd.Data.Text = "Осматриваю мост"
	if err := s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	saved, err := s.load(ctx, r.ID, 1)
	if err != nil || saved.State.Turn != 1 {
		t.Fatal("first action missing", err)
	}
	events, _ := s.eventList(ctx, r.ID)
	var notices int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM telegram_outbox").Scan(&notices); err != nil {
		t.Fatal(err)
	}
	restarted := &Server{Config: s.Config, Pool: s.Pool, AI: s.AI}
	if status := restarted.queryCommand(ctx, r.ID, 1, cmd.ID); status.Status != "completed" {
		t.Fatal("lost acknowledgement cannot be recovered", status)
	}
	if err := restarted.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal("successful command not replayable after turn changed", err)
	}
	current, err := s.load(ctx, r.ID, 1)
	if err != nil || string(blob(current.State)) != string(blob(saved.State)) || calls != 1 {
		t.Fatal("duplicate changed game state", calls, err)
	}
	currentEvents, _ := s.eventList(ctx, r.ID)
	var currentNotices int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM telegram_outbox").Scan(&currentNotices); err != nil {
		t.Fatal(err)
	}
	if len(currentEvents) != len(events) || currentNotices != notices {
		t.Fatal("duplicate history/notifications")
	}
	cmd.Data.Text = "Другое действие"
	var apiErr *apiError
	if err := s.command(ctx, r.ID, 1, cmd); !errors.As(err, &apiErr) || apiErr.Code != "COMMAND_ID_REUSED" {
		t.Fatal("ID accepted with different payload", err)
	}
	if s.queryCommand(ctx, r.ID, 2, cmd.ID).Status != "unknown" || s.queryCommand(ctx, r.ID, 999, cmd.ID).Status != "unknown" {
		t.Fatal("another user can read receipt")
	}
}

func TestFailedCommandCanRetryButStaleActionsCannotExecute(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	cmd := Command{ID: uuid.NewString(), Type: "player_action"}
	cmd.Data.Text = "Смотрю"
	s.AI.(*plannedAI).actions = []game.Action{{Type: "UNKNOWN"}}
	if err := s.command(ctx, r.ID, 1, cmd); err == nil {
		t.Fatal("invalid turn succeeded")
	}
	var count int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM command_receipts").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed action stored success receipt", err)
	}
	s.AI.(*plannedAI).actions = nil
	if err := s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal("retry failed", err)
	}
	old := 0
	cmd.ID, cmd.ExpectedTurn = uuid.NewString(), &old
	if err := s.command(ctx, r.ID, 1, cmd); err == nil {
		t.Fatal("stale action changed world")
	}
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM command_receipts").Scan(&count); err != nil || count != 1 {
		t.Fatal("stale action stored receipt", err)
	}
}

func TestDiceAndPotionAreNotRepeated(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	if _, err := s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error { r.State.Hero(1).HP = 1; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"roll_dice", "use_item"} {
		cmd := Command{ID: uuid.NewString(), Type: typ}
		cmd.Data.Notation = "d20"
		cmd.Data.ItemID = r.State.Hero(1).Inventory[0].ID
		if err := s.command(ctx, r.ID, 1, cmd); err != nil {
			t.Fatal(err)
		}
		before, _ := s.load(ctx, r.ID, 1)
		events, _ := s.eventList(ctx, r.ID)
		if err := s.command(ctx, r.ID, 1, cmd); err != nil {
			t.Fatal(err)
		}
		after, _ := s.load(ctx, r.ID, 1)
		afterEvents, _ := s.eventList(ctx, r.ID)
		if string(blob(before.State)) != string(blob(after.State)) || len(events) != len(afterEvents) {
			t.Fatal("duplicate rerolled or consumed item", typ)
		}
	}
}

func TestSocketCommandReconnectAndDuplicateWhileProcessing(t *testing.T) {
	ctx, s, httpServer := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	started, release := make(chan struct{}), make(chan struct{})
	calls := 0
	s.AI = openingProvider(func(ctx context.Context, _ string, _ ai.Input) (ai.Output, error) {
		calls++
		if calls == 1 {
			close(started)
		}
		select {
		case <-release:
			return ai.Output{Narrative: "Ответ мастера", Actions: []game.Action{}}, nil
		case <-ctx.Done():
			return ai.Output{}, ctx.Err()
		}
	})
	connect := func() *websocket.Conn {
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws/rooms/"+r.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		if err := wsjson.Write(ctx, conn, Frame{"auth", map[string]string{"token": auth.Issue(auth.User{ID: 1}, s.Config.SessionSecret, time.Now())}}); err != nil {
			t.Fatal(err)
		}
		var initial Frame
		if err := wsjson.Read(ctx, conn, &initial); err != nil || initial.Type != "room_state" {
			t.Fatal(initial, err)
		}
		return conn
	}
	waitStatus := func(conn *websocket.Conn, expected string) commandStatus {
		for {
			var frame struct {
				Type string        `json:"type"`
				Data commandStatus `json:"data"`
			}
			if err := wsjson.Read(ctx, conn, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Type == "command_status" && frame.Data.Status == expected {
				return frame.Data
			}
		}
	}
	cmd := Command{ID: uuid.NewString(), Type: "player_action"}
	cmd.Data.Text = "Осматриваюсь"
	first := connect()
	if err := wsjson.Write(ctx, first, cmd); err != nil {
		t.Fatal(err)
	}
	if got := waitStatus(first, "queued"); got.ID != cmd.ID {
		t.Fatal(got)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("action not started")
	}
	first.CloseNow()
	second := connect()
	if err := wsjson.Write(ctx, second, cmd); err != nil {
		t.Fatal(err)
	}
	waitStatus(second, "processing")
	close(release)
	waitStatus(second, "completed")
	second.CloseNow()
	third := connect()
	if err := wsjson.Write(ctx, third, Command{Type: "command_status", ID: cmd.ID}); err != nil {
		t.Fatal(err)
	}
	waitStatus(third, "completed")
	loaded, err := s.load(ctx, r.ID, 1)
	if err != nil || loaded.State.Turn != 1 || calls != 1 {
		t.Fatal("reconnect duplicated turn", calls, err)
	}
}
