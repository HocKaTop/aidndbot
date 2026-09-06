package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

func (s *Server) command(ctx context.Context, rid string, user int64, cmd Command) error {
	if cmd.Type == "start_game" {
		return s.startGame(ctx, rid, user)
	}
	_, e := s.mutate(ctx, rid, user, false, func(q *store.Queries, r *Room) error {
		if cmd.Type == "ready" {
			if r.Status != "WAITING" {
				return bad("Игра уже началась")
			}
			if r.State.Hero(user) == nil {
				return bad("Сначала создай персонажа")
			}
			u, _ := id(rid)
			return q.SetReady(ctx, store.SetReadyParams{RoomID: u, UserID: user, Ready: cmd.Data.Ready})
		}
		if r.Status != "PLAYING" {
			return bad("Игра ещё не началась или на паузе")
		}
		hero := r.State.Hero(user)
		if hero == nil || hero.HP <= 0 {
			return bad("Нужен живой персонаж")
		}
		switch cmd.Type {
		case "roll_dice":
			roll, e := game.RollDice(cmd.Data.Notation)
			if e != nil {
				return bad(e.Error())
			}
			return s.addEvent(ctx, q, rid, user, "DICE_ROLL", game.Result{Type: "DICE_ROLL", Text: fmt.Sprintf("%s бросает %s: %d", hero.Name, roll.Notation, roll.Total), Roll: &roll})
		case "use_item":
			out, e := game.UseItem(&r.State, user, cmd.Data.ItemID)
			if e != nil {
				return bad(e.Error())
			}
			return s.addEvent(ctx, q, rid, user, "ITEM_USED", out)
		case "player_action":
			text := strings.TrimSpace(cmd.Data.Text)
			if text == "" || len(text) > 2000 {
				return bad("Действие должно содержать 1–2000 байт")
			}
			ridUUID, _ := id(rid)
			events, e := q.ListEvents(ctx, ridUUID)
			if e != nil {
				return e
			}
			recent := []string{}
			start := max(0, len(events)-20)
			for _, v := range events[start:] {
				recent = append(recent, string(v.Payload))
			}
			input := ai.Input{State: r.State, Recent: recent, PlayerID: user, Text: text}
			started := time.Now()
			out, e := s.AI.GenerateTurn(ctx, r.State.Settings.OllamaModel, input)
			slog.Info("AI request", "room", rid, "duration", time.Since(started), "success", e == nil)
			if e != nil {
				slog.Warn("AI failed", "room", rid, "error", e)
				return bad("Ollama не ответила корректно. Проверь модель и соединение; ход не сохранён.")
			}
			results := []game.Result{}
			mechanical := 0
			for _, a := range out.Actions {
				if a.Type == "ATTACK" || a.Type == "SKILL_CHECK" || a.Type == "DICE_ROLL" {
					mechanical++
				}
				if mechanical > 1 {
					return bad("Модель запросила несколько проверок за ход. Повтори действие.")
				}
				result, e := game.Apply(&r.State, user, a)
				if e != nil {
					slog.Warn("AI action rejected", "room", rid, "type", a.Type, "reason", e)
					return bad("Модель предложила недопустимое действие. Ход не сохранён; попробуй уточнить запрос.")
				}
				results = append(results, result)
			}
			retaliation, e := game.Retaliate(&r.State, user)
			if e != nil {
				return e
			}
			results = append(results, retaliation...)
			if len(results) > 0 {
				input.State = r.State
				input.Results = results
				narration, e := s.AI.GenerateTurn(ctx, r.State.Settings.OllamaModel, input)
				if e != nil {
					slog.Warn("AI narration failed", "room", rid, "error", e)
					return bad("Не удалось завершить повествование. Ход не сохранён.")
				}
				out.Narrative = narration.Narrative
			}
			if strings.TrimSpace(out.Narrative) == "" {
				return bad("Модель вернула пустое повествование. Ход не сохранён.")
			}
			if e = s.addEvent(ctx, q, rid, user, "PLAYER_ACTION", game.Result{Type: "PLAYER_ACTION", Text: hero.Name + ": " + text}); e != nil {
				return e
			}
			for _, result := range results {
				if e = s.addEvent(ctx, q, rid, user, result.Type, result); e != nil {
					return e
				}
			}
			if e = s.addEvent(ctx, q, rid, 0, "GM_MESSAGE", game.Result{Type: "GM_MESSAGE", Text: out.Narrative}); e != nil {
				return e
			}
			r.State.Turn++
			// Compact factual memory is built only from validated engine changes, not model claims.
			for _, result := range results {
				r.State.Summary += fmt.Sprintf("\nХод %d: %s", r.State.Turn, result.Text)
			}
			runes := []rune(r.State.Summary)
			if len(runes) > 6000 {
				r.State.Summary = string(runes[len(runes)-6000:])
			}
			return nil
		}
		return bad("Неизвестная команда")
	})
	return e
}
