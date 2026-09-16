package server

import (
	"dnd-bot/backend/internal/auth"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestFinishCampaignPersistsOnceAndFreezesWorld(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	r, err := s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.Status = "PAUSED"
		r.State.Turn = 7
		r.State.Combat = true
		r.State.EnsureCombatOrder(1, r.State.CombatTurnSince)
		r.State.Quests = []game.Quest{{ID: uuid.NewString(), Title: "Спасти мост", Status: "ACTIVE"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command{ID: uuid.NewString(), Type: "finish_game", ExpectedTurn: &r.State.Turn}
	cmd.Data.Code, cmd.Data.Text = r.Code, "Мы вернулись домой."
	if err = s.command(ctx, r.ID, 2, cmd); err == nil {
		t.Fatal("non-owner finished campaign")
	}
	cmd.Data.Code = "WRONG"
	if err = s.command(ctx, r.ID, 1, cmd); err == nil {
		t.Fatal("confirmation not required")
	}
	cmd.Data.Code = r.Code
	cmd.Data.Text = strings.Repeat("я", 1001)
	if err = s.command(ctx, r.ID, 1, cmd); err == nil {
		t.Fatal("unbounded ending")
	}
	cmd.Data.Text = "Мы вернулись домой."
	old := r.State.Turn - 1
	cmd.ExpectedTurn = &old
	if err = s.command(ctx, r.ID, 1, cmd); err == nil {
		t.Fatal("stale finish accepted")
	}
	cmd.ExpectedTurn = &r.State.Turn
	if err = s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	restored := &Server{Config: s.Config, Pool: s.Pool}
	saved, err := restored.load(ctx, r.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "FINISHED" || saved.State.Ending == nil || saved.State.Ending.Reason != "owner" || saved.State.Ending.Note != cmd.Data.Text || saved.State.Ending.FinishedAt.IsZero() {
		t.Fatal("ending not restored", saved, err)
	}
	if saved.State.Turn != 7 || saved.State.Combat || len(saved.State.CombatOrder) != 0 || saved.State.Quests[0].Status != "ACTIVE" {
		t.Fatal("ending changed turns/quests or left combat active")
	}
	events, err := s.eventList(ctx, r.ID)
	if err != nil || len(events) != 1 || events[0].Type != "GAME_FINISHED" {
		t.Fatal("ending event missing", events, err)
	}
	var notices int
	if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM telegram_outbox WHERE room_id=$1 AND body LIKE '%Итоги приключения%'", r.ID).Scan(&notices); err != nil || notices != 2 {
		t.Fatal("final notifications missing", notices, err)
	}
	if err = restored.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal("retry not idempotent", err)
	}
	if err = restored.startGame(ctx, r.ID, 1); err == nil {
		t.Fatal("finished campaign resumed")
	}
	for _, typ := range []string{"player_action", "use_item", "roll_dice", "pass_turn", "skip_turn", "ready", "finish_game"} {
		blocked := Command{Type: typ}
		blocked.Data.Code, blocked.Data.Text = r.Code, "Продолжаем"
		if err = restored.command(ctx, r.ID, 1, blocked); err == nil {
			t.Fatal("finished world mutated", typ)
		}
	}
	after, err := restored.load(ctx, r.ID, 1)
	if err != nil || string(blob(saved.State)) != string(blob(after.State)) {
		t.Fatal("finished world changed", err)
	}
	afterEvents, err := s.eventList(ctx, r.ID)
	if err != nil || len(afterEvents) != len(events) {
		t.Fatal("duplicate ending events", err)
	}
	var afterNotices int
	if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM telegram_outbox WHERE room_id=$1", r.ID).Scan(&afterNotices); err != nil || afterNotices != notices {
		t.Fatal("duplicate final notifications", err)
	}
}

func TestTextFinishRequiresConfirmationAndExplainsFinishedState(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	u := auth.User{ID: 1, FirstName: "Tester"}
	before, err := s.TextMessage(ctx, u, "/finish")
	if err != nil || !strings.Contains(before, "/finish "+r.Code) {
		t.Fatal(before, err)
	}
	stillPlaying, _ := s.load(ctx, r.ID, 1)
	if stillPlaying.Status != "PLAYING" {
		t.Fatal("prompt finished campaign")
	}
	out, err := s.TextMessage(ctx, u, "/finish "+r.Code)
	if err != nil || !strings.Contains(out, "Итоги приключения") || !strings.Contains(out, "Ходов: 0") {
		t.Fatal(out, err)
	}
	for _, message := range []string{"/state", "/finish", "Я открываю дверь"} {
		out, err = s.TextMessage(ctx, u, message)
		if err != nil || !strings.Contains(out, "Итоги приключения") {
			t.Fatal(message, out, err)
		}
	}
	out, err = s.TextMessage(ctx, u, "/play")
	if err != nil || !strings.Contains(out, "Кампания завершена") {
		t.Fatal(out, err)
	}
	lobby := testCampaign(t, ctx, s, 3)
	cmd := Command{Type: "finish_game"}
	cmd.Data.Code = lobby.Code
	if err = s.command(ctx, lobby.ID, 3, cmd); err == nil {
		t.Fatal("unstarted campaign finished")
	}
}

func TestDefeatEndingSurvivesReload(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	_, err := s.mutate(ctx, r.ID, 1, false, func(q *store.Queries, r *Room) error {
		for i := range r.State.Characters {
			r.State.Characters[i].HP = 0
		}
		r.State.Combat = true
		results, err := settleTurn(r, 1, true, nil)
		if err != nil {
			return err
		}
		rememberTurn(&r.State, results)
		for _, result := range results {
			if err = s.addEvent(ctx, q, r.ID, 1, result.Type, result); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.load(ctx, r.ID, 2)
	if err != nil || loaded.Status != "FINISHED" || loaded.State.Ending == nil || loaded.State.Ending.Reason != "defeat" || loaded.State.Turn != 1 {
		t.Fatal("defeat ending missing", loaded, err)
	}
	if !strings.Contains(describeEnding(loaded), "Выжило героев: 0/2") {
		t.Fatal("wrong defeat results")
	}
	loaded.State.Ending = nil // Old FINISHED snapshots remain readable.
	if !strings.Contains(describeEnding(loaded), "Весь отряд пал") {
		t.Fatal("legacy defeat lost")
	}
}
