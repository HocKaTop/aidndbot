package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/auth"
	"dnd-bot/backend/internal/bot"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/notifications"
	"dnd-bot/backend/internal/store"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func multiplayerRoom(t *testing.T, ctx context.Context, s *Server) Room {
	t.Helper()
	r := testCampaign(t, ctx, s, 1)
	if _, err := s.textMessage(ctx, auth.User{ID: 2, FirstName: "Анна"}, "/join "+r.Code); err != nil {
		t.Fatal(err)
	}
	r, err := s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.Status = "PLAYING"
		r.State.Scene = &game.Scene{ID: "d00a7ae4-e064-4204-ae19-b9edbec2fb4f", Title: "Мост", Location: "Мост"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRoomsRunIndependentlyAndExposeActivity(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	other := testCampaign(t, ctx, s, 3)
	if _, err := s.mutate(ctx, other.ID, 3, false, func(_ *store.Queries, r *Room) error { r.Status = "PLAYING"; return nil }); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	s.AI = openingProvider(func(ctx context.Context, _ string, in ai.Input) (ai.Output, error) {
		if in.PlayerID == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ai.Output{}, ctx.Err()
			}
		}
		return ai.Output{Narrative: "Мастер отвечает.", Actions: []game.Action{}}, nil
	})
	cmd := Command{Type: "player_action"}
	cmd.Data.Text = "Смотрю вокруг"
	done := make(chan error, 1)
	go func() { done <- s.command(ctx, r.ID, 1, cmd) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("turn did not start")
	}
	loaded, err := s.load(ctx, r.ID, 2)
	if err != nil || !loaded.Activity.Processing || loaded.Activity.UserID != 1 || loaded.Activity.Name != "Tester" {
		t.Fatal("activity missing", loaded.Activity, err)
	}
	// A reconnect receives the current activity; closing a client cannot consume a turn.
	c := &client{user: 2, out: make(chan Frame, 8)}
	s.Hub.add(r.ID, c)
	s.Hub.sendActivity(r.ID, c)
	frame := <-c.out
	if frame.Type != "room_activity" || !frame.Data.(Activity).Processing {
		t.Fatal(frame)
	}
	s.Hub.remove(r.ID, c)
	fastCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.command(fastCtx, other.ID, 3, cmd); err != nil {
		t.Fatal("other room blocked", err)
	}
	err = s.command(fastCtx, r.ID, 2, cmd)
	var apiErr *apiError
	if !errors.As(err, &apiErr) || apiErr.Code != "ROOM_BUSY" {
		t.Fatal("same room should reject concurrent action promptly", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.Hub.Activity(r.ID).Processing {
		t.Fatal("activity not cleared")
	}
}

func TestNotificationsCommitRetryAndSubscriptions(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	user := auth.User{ID: 1, FirstName: "Tester"}
	text, err := s.textMessage(ctx, user, "Осматриваю мост")
	if err != nil || !strings.Contains(text, "Ход.") {
		t.Fatal(text, err)
	}
	var count int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM telegram_outbox").Scan(&count); err != nil || count != 1 {
		t.Fatal("expected only the other participant", count, err)
	}
	var recipient int64
	var delivered string
	work, err := notifications.DeliverOne(ctx, s.Pool, func(_ context.Context, id int64, text string) error {
		recipient, delivered = id, text
		return errors.New("temporary network failure")
	})
	if !work || err == nil || recipient != 2 || !strings.Contains(delivered, "Осматриваю мост") || !strings.Contains(delivered, r.Code) {
		t.Fatal("wrong event recipient/body", recipient, delivered, err)
	}
	var attempts int
	if err := s.Pool.QueryRow(ctx, "SELECT attempts FROM telegram_outbox").Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("failed delivery not retained", err)
	}
	// A new room's notification proceeds while the earlier recipient is backing off.
	other := testCampaign(t, ctx, s, 3)
	_, err = s.mutate(ctx, other.ID, 3, false, func(q *store.Queries, r *Room) error {
		return s.addEvent(ctx, q, r.ID, 3, "GAME_STARTED", game.Result{Text: "Другая комната"})
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err = notifications.DeliverOne(ctx, s.Pool, func(_ context.Context, id int64, _ string) error {
		if id != 3 {
			t.Errorf("retry blocked independent recipient: %d", id)
		}
		return nil
	})
	if !work || err != nil {
		t.Fatal(work, err)
	}
	if _, err := s.Pool.Exec(ctx, "UPDATE telegram_outbox SET next_attempt=now()"); err != nil {
		t.Fatal(err)
	}
	work, err = notifications.DeliverOne(ctx, s.Pool, func(_ context.Context, id int64, _ string) error {
		if id != 2 {
			t.Errorf("wrong retry recipient: %d", id)
		}
		return nil
	})
	if !work || err != nil {
		t.Fatal(work, err)
	}
	// Failed game plans must never enqueue speculative events.
	s.AI.(*plannedAI).actions = []game.Action{{Type: "UNKNOWN"}}
	if _, err := s.textMessage(ctx, user, "Неудачный ход"); err == nil {
		t.Fatal("invalid plan accepted")
	}
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM telegram_outbox").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed turn queued events", count, err)
	}
	s.AI.(*plannedAI).actions = nil
	if _, err := s.textMessage(ctx, user, "Снова смотрю"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.textMessage(ctx, auth.User{ID: 2, FirstName: "Анна"}, "/mute"); err != nil {
		t.Fatal(err)
	}
	work, err = notifications.DeliverOne(ctx, s.Pool, func(context.Context, int64, string) error { t.Error("muted user received event"); return nil })
	if !work || err != nil {
		t.Fatal(work, err)
	}
	if _, err := s.textMessage(ctx, auth.User{ID: 2, FirstName: "Анна"}, "/unmute"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.textMessage(ctx, user, "Ещё один ход"); err != nil {
		t.Fatal(err)
	}
	_, _ = notifications.DeliverOne(ctx, s.Pool, func(context.Context, int64, string) error { return &bot.APIError{Code: 403} })
	var enabled bool
	if err := s.Pool.QueryRow(ctx, "SELECT notifications FROM bot_sessions WHERE user_id=2").Scan(&enabled); err != nil || enabled {
		t.Fatal("blocked bot kept retrying", err)
	}
}

func TestCombatTurnsSkipAndPersistence(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	_, err := s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.State.Combat = true
		r.State.NPCs = []game.NPC{{ID: "3c1f9f12-1d85-4d7b-9c4d-b0a4f3bdad27", Name: "Враг", HP: 12, MaxHP: 12, Alive: true, Disposition: "hostile", Location: "Мост"}}
		r.State.EnsureCombatOrder(1, time.Now())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command{Type: "player_action"}
	cmd.Data.Text = "Смотрю"
	if err = s.command(ctx, r.ID, 2, cmd); err == nil {
		t.Fatal("out-of-turn action accepted")
	}
	cmd.Type = "use_item"
	cmd.Data.ItemID = r.State.Hero(2).Inventory[0].ID
	if err = s.command(ctx, r.ID, 2, cmd); err == nil {
		t.Fatal("out-of-turn potion accepted")
	}
	cmd.Type = "pass_turn"
	if err = s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.load(ctx, r.ID, 1)
	if err != nil || loaded.State.CombatHero().UserID != 2 || loaded.State.Turn != 1 {
		t.Fatal("turn did not advance", err)
	}
	cmd.Type = "skip_turn"
	cmd.Data.Turn = loaded.State.Turn
	if err = s.command(ctx, r.ID, 1, cmd); err == nil {
		t.Fatal("owner skipped without waiting")
	}
	_, err = s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.State.CombatTurnSince = time.Now().Add(-2 * time.Minute)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.command(ctx, r.ID, 2, cmd); err == nil {
		t.Fatal("non-owner skipped another turn")
	}
	if err = s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	if err = s.command(ctx, r.ID, 1, cmd); err == nil {
		t.Fatal("stale skip consumed another turn")
	}
	restored := &Server{Pool: s.Pool, Config: s.Config}
	loaded, err = restored.load(ctx, r.ID, 1)
	if err != nil || loaded.State.CombatHero().UserID != 1 || loaded.State.CombatRound != 2 || loaded.State.Turn != 2 {
		t.Fatal("combat order not restored", err)
	}
	events, err := s.eventList(ctx, r.ID)
	if err != nil || len(events) != 6 || events[4].Type != "NPC_ATTACK" || events[5].Type != "TURN_CHANGED" {
		t.Fatal("skipping bypassed retaliation", events, err)
	}
}

func TestDefendTurnPersistsAndUsesHigherArmor(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	r, err := s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.State.Combat = true
		r.State.NPCs = []game.NPC{{ID: "3c1f9f12-1d85-4d7b-9c4d-b0a4f3bdad27", Name: "Враг", HP: 12, MaxHP: 12, Alive: true, Disposition: "hostile", Location: "Мост"}}
		r.State.EnsureCombatOrder(1, time.Now())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command{Type: "defend_turn", ExpectedTurn: &r.State.Turn}
	if err = s.command(ctx, r.ID, 2, cmd); err == nil {
		t.Fatal("other hero defended out of turn")
	}
	if err = s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.load(ctx, r.ID, 1)
	if err != nil || loaded.State.Turn != 1 || loaded.State.CombatHero().UserID != 2 || loaded.State.NPCResponseCount != 1 {
		t.Fatal("defense did not save turn and enemy response", err)
	}
	events, err := s.eventList(ctx, r.ID)
	if err != nil || len(events) != 3 || events[0].Type != "DEFEND" || events[1].Type != "NPC_ATTACK" || events[2].Type != "TURN_CHANGED" {
		t.Fatal("defense events missing", events, err)
	}
	var attack game.Result
	if err = json.Unmarshal(events[1].Payload, &attack); err != nil || attack.Attack == nil {
		t.Fatal("enemy roll missing", err)
	}
	if attack.Attack.Hit != game.Hits(attack.Attack.Roll.Total, 2, 15) || loaded.State.Hero(1).HP != 20-attack.Attack.Damage {
		t.Fatal("defense bonus ignored", attack, loaded.State.Hero(1).HP)
	}
}

func TestConcurrentNotificationWorkersPreserveRecipientOrder(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	for _, message := range []string{"Первый ход", "Второй ход"} {
		if _, err := s.textMessage(ctx, auth.User{ID: 1, FirstName: "Tester"}, message); err != nil {
			t.Fatal(err)
		}
	}
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := notifications.DeliverOne(ctx, s.Pool, func(ctx context.Context, id int64, text string) error {
			if id != 2 || !strings.Contains(text, "Первый ход") {
				t.Errorf("first message wrong: %d %s", id, text)
			}
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("delivery did not start")
	}
	work, err := notifications.DeliverOne(ctx, s.Pool, func(context.Context, int64, string) error {
		t.Error("second worker overtook first message")
		return nil
	})
	if work || err != nil {
		t.Fatal("locked recipient was not skipped", work, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	work, err = notifications.DeliverOne(ctx, s.Pool, func(_ context.Context, id int64, text string) error {
		if id != 2 || !strings.Contains(text, "Второй ход") {
			t.Errorf("second message wrong: %d %s", id, text)
		}
		return nil
	})
	if !work || err != nil {
		t.Fatal(work, err)
	}
	// Switching the selected campaign cancels its old pending notifications.
	if _, err := s.textMessage(ctx, auth.User{ID: 1, FirstName: "Tester"}, "Старое уведомление"); err != nil {
		t.Fatal(err)
	}
	other := testCampaign(t, ctx, s, 2)
	if other.ID == r.ID {
		t.Fatal("test did not switch rooms")
	}
	_, err = notifications.DeliverOne(ctx, s.Pool, func(context.Context, int64, string) error { t.Error("old subscription was delivered"); return nil })
	if err != nil {
		t.Fatal(err)
	}
}

func TestCombatClockStartsAfterNarration(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	_, err := s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.State.Combat = true
		r.State.NPCs = []game.NPC{{ID: "3c1f9f12-1d85-4d7b-9c4d-b0a4f3bdad27", Name: "Враг", HP: 12, MaxHP: 12, Alive: true, Disposition: "hostile", Location: "Мост"}}
		r.State.EnsureCombatOrder(1, time.Now())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var narrationFinished time.Time
	s.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		if in.Results != nil {
			narrationFinished = time.Now()
		}
		return ai.Output{Narrative: "Мастер описал события.", Actions: []game.Action{}}, nil
	})
	cmd := Command{Type: "player_action"}
	cmd.Data.Text = "Наблюдаю за врагом"
	if err := s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.load(ctx, r.ID, 1)
	if err != nil || narrationFinished.IsZero() || !loaded.State.CombatTurnSince.After(narrationFinished) {
		t.Fatal("model latency consumed the next player's time", err)
	}
}
