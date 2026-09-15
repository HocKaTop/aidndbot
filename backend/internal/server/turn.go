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
	"unicode/utf8"
)

func (s *Server) command(ctx context.Context, rid string, user int64, cmd Command) error {
	ctx = context.WithValue(ctx, operationKey{}, cmd.Type)
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
		if cmd.Type == "skip_turn" {
			return s.skipCombatTurn(ctx, q, r, user, cmd.Data.Turn)
		}
		if hero == nil || hero.HP <= 0 {
			return bad("Нужен живой персонаж")
		}
		if current := r.State.CombatHero(); current != nil && current.UserID != user && cmd.Type != "roll_dice" {
			return bad("Сейчас ходит " + current.Name + ". Дождись своей очереди.")
		}
		switch cmd.Type {
		case "pass_turn":
			if !r.State.Combat {
				return bad("Пропустить ход можно только в бою")
			}
			return s.passCombatTurn(ctx, q, r, user, hero.Name+" пропускает ход.")
		case "roll_dice":
			roll, e := game.RollDice(cmd.Data.Notation)
			if e != nil {
				return bad(e.Error())
			}
			return s.addEvent(ctx, q, rid, user, "DICE_ROLL", game.Result{Type: "DICE_ROLL", Text: fmt.Sprintf("%s бросает %s: %d", hero.Name, roll.Notation, roll.Total), Roll: &roll})
		case "use_item":
			wasCombat := r.State.Combat
			out, e := game.UseItem(&r.State, user, cmd.Data.ItemID)
			if e != nil {
				return bad(e.Error())
			}
			results, e := settleTurn(r, user, wasCombat, []game.Result{out})
			if e != nil {
				return e
			}
			rememberTurn(&r.State, results)
			for _, result := range results {
				if e = s.addEvent(ctx, q, rid, user, result.Type, result); e != nil {
					return e
				}
			}
			return nil
		case "player_action":
			text := strings.TrimSpace(cmd.Data.Text)
			if text == "" || utf8.RuneCountInString(text) > 1000 {
				return bad("Действие должно содержать 1–1000 символов")
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
			out, e := s.planTurn(ctx, rid, input)
			if e != nil {
				return e
			}
			wasCombat := r.State.Combat
			results, e := game.ApplyActions(&r.State, user, out.Actions)
			if e != nil {
				slog.Warn("AI action rejected", "room", rid, "reason", e)
				return bad("Модель предложила недопустимое действие. Ход не сохранён; попробуй уточнить запрос.")
			}
			results, e = settleTurn(r, user, wasCombat, results)
			if e != nil {
				return e
			}
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
			rememberTurn(&r.State, results)
			return nil
		}
		return bad("Неизвестная команда")
	})
	return e
}

func settleTurn(r *Room, user int64, wasCombat bool, results []game.Result) ([]game.Result, error) {
	// Also repair old snapshots where enemies in another scene kept combat active.
	if r.State.Combat && !r.State.HasEnemies() {
		r.State.Combat = false
	}
	r.State.EnsureCombatOrder(user, time.Now())
	retaliation, err := game.Retaliate(&r.State, user)
	if err != nil {
		return nil, err
	}
	results = append(results, retaliation...)
	if r.State.PartyDefeated() {
		r.State.Combat = false
		r.Status = "FINISHED"
		results = append(results, game.Result{Type: "GAME_FINISHED", Text: "Весь отряд пал. Кампания завершена; её историю можно перечитать."})
	} else if wasCombat && !r.State.Combat {
		results = append(results, game.Result{Type: "COMBAT_ENDED", Text: "Все противники в текущей сцене побеждены. Бой завершён."})
	}
	r.State.AdvanceCombatTurn(time.Now())
	if current := r.State.CombatHero(); current != nil {
		results = append(results, game.Result{Type: "TURN_CHANGED", Text: fmt.Sprintf("Раунд %d. Ходит %s.", r.State.CombatRound, current.Name)})
	}
	return results, nil
}

func (s *Server) passCombatTurn(ctx context.Context, q *store.Queries, r *Room, user int64, text string) error {
	results, err := settleTurn(r, user, true, []game.Result{{Type: "TURN_PASSED", Text: text}})
	if err != nil {
		return err
	}
	rememberTurn(&r.State, results)
	for _, result := range results {
		if err = s.addEvent(ctx, q, r.ID, user, result.Type, result); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) skipCombatTurn(ctx context.Context, q *store.Queries, r *Room, owner int64, expectedTurn int) error {
	if !CanManage(r.OwnerID, owner) {
		return denied()
	}
	hero := r.State.CombatHero()
	if hero == nil {
		return bad("Сейчас нет боевого хода")
	}
	if expectedTurn != r.State.Turn {
		return bad("Очередь уже изменилась. Проверь, кто ходит сейчас.")
	}
	if time.Since(r.State.CombatTurnSince) < time.Minute {
		return bad("Дай игроку минуту на действие или возвращение в игру.")
	}
	return s.passCombatTurn(ctx, q, r, hero.UserID, "Владелец пропустил ход игрока "+hero.Name+" после ожидания.")
}
func rememberTurn(state *game.State, results []game.Result) {
	state.Turn++
	for _, result := range results {
		state.Summary += fmt.Sprintf("\nХод %d: %s", state.Turn, result.Text)
	}
	runes := []rune(state.Summary)
	if len(runes) > 6000 {
		state.Summary = string(runes[len(runes)-6000:])
	}
}
