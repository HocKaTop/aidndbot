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
	identity, err := identifyCommand(cmd)
	if err != nil {
		return err
	}
	if identity != nil {
		ctx = context.WithValue(ctx, commandKey{}, identity)
	}
	ctx = context.WithValue(ctx, operationKey{}, cmd.Type)
	if cmd.Type == "start_game" {
		return s.startGame(ctx, rid, user)
	}
	_, e := s.mutate(ctx, rid, user, false, func(q *store.Queries, r *Room) error {
		if cmd.ExpectedTurn != nil && *cmd.ExpectedTurn != r.State.Turn {
			return bad("Ситуация уже изменилась. Посмотри последние события и отправь новое действие.")
		}
		if cmd.Type == "finish_game" {
			return s.finishCampaign(ctx, q, r, user, cmd.Data.Code, cmd.Data.Text)
		}
		if r.Status == "FINISHED" {
			return bad("Кампания завершена. Можно перечитать историю или создать новое приключение.")
		}
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
		if cmd.Type == "confirm_quest" || cmd.Type == "continue_quest" {
			return s.resolveQuestProposal(ctx, q, r, user, cmd.Type)
		}
		if cmd.Type == "reopen_quest" {
			return s.reopenQuest(ctx, q, r, user, cmd.Data.Target)
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
		if r.State.PendingQuestCompletion != nil && cmd.Type != "roll_dice" {
			r.State.PendingQuestCompletion = nil
			if e := s.addEvent(ctx, q, rid, user, "QUEST_COMPLETION_CONTINUED", game.Result{Type: "QUEST_COMPLETION_CONTINUED", Text: "Отряд продолжает приключение; цель пока активна."}); e != nil {
				return e
			}
		}
		switch cmd.Type {
		case "claim_stone", "return_to_bridge", "install_stone":
			return s.lanternCommand(ctx, q, r, user, cmd.Type)
		case "pass_turn":
			if !r.State.Combat {
				return bad("Пропустить ход можно только в бою")
			}
			return s.passCombatTurn(ctx, q, r, user, hero.Name+" пропускает ход.")
		case "defend_turn":
			if !r.State.Combat {
				return bad("Защищаться можно только в бою")
			}
			results, e := settleTurn(r, user, true, []game.Result{{Type: "DEFEND", Text: hero.Name + " защищается: +2 AC против следующей ответной атаки."}})
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
		case "class_ability":
			wasCombat := r.State.Combat
			results, e := game.UseAbility(&r.State, user, cmd.Data.Ability, cmd.Data.Target)
			if e != nil {
				return bad(e.Error())
			}
			results, e = settleTurn(r, user, wasCombat, results)
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
		case "attack_npc":
			npc := r.State.NPC(cmd.Data.Target)
			if npc == nil || !npc.Alive || !r.State.Present(*npc) {
				return bad("Цель атаки недоступна")
			}
			wasCombat := r.State.Combat
			actions := []game.Action{}
			if npc.Disposition != "hostile" {
				actions = append(actions, game.Action{Type: "SET_DISPOSITION", Target: npc.ID, Status: "hostile"})
			}
			if !r.State.Combat {
				actions = append(actions, game.Action{Type: "START_COMBAT"})
			}
			actions = append(actions, game.Action{Type: "ATTACK", Target: npc.ID})
			results, e := game.ApplyActions(&r.State, user, actions)
			if e != nil {
				return bad(e.Error())
			}
			results, e = settleTurn(r, user, wasCombat, results)
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
			wasCombat := r.State.Combat
			results, explicitAttack, e := applyExplicitAttack(&r.State, user, text)
			if e != nil {
				return e
			}
			var out ai.Output
			if !explicitAttack {
				out, e = s.planTurn(ctx, rid, input)
				if e != nil {
					return e
				}
				results, e = game.ApplyActions(&r.State, user, out.Actions)
				if e != nil {
					slog.Warn("AI action rejected", "room", rid, "reason", e)
					return bad("Модель предложила недопустимое действие. Ход не сохранён; попробуй уточнить запрос.")
				}
			}
			if appeared, err := ensureLanternThief(&r.State, user); err != nil {
				return err
			} else if appeared != nil {
				results = append(results, *appeared)
			}
			results, e = settleTurn(r, user, wasCombat, results)
			if e != nil {
				return e
			}
			if explicitAttack {
				out.Narrative = combatNarrative(results)
			} else if len(results) > 0 {
				input.State = r.State
				input.Results = results
				narration, e := s.narrateTurn(ctx, rid, input)
				if e != nil {
					return e
				}
				out.Narrative = narration.Narrative
				out.Memory = narration.Memory
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
			rememberGMNotes(&r.State, out.Memory)
			return nil
		}
		return bad("Неизвестная команда")
	})
	return e
}

func (s *Server) resolveQuestProposal(ctx context.Context, q *store.Queries, r *Room, user int64, kind string) error {
	if !CanManage(r.OwnerID, user) {
		return denied()
	}
	proposal := r.State.PendingQuestCompletion
	if proposal == nil {
		return bad("Предложение завершить цель уже неактуально")
	}
	if kind == "continue_quest" {
		r.State.PendingQuestCompletion = nil
		return s.addEvent(ctx, q, r.ID, user, "QUEST_COMPLETION_CONTINUED", game.Result{Type: "QUEST_COMPLETION_CONTINUED", Text: "Отряд решил продолжить: цель остаётся активной."})
	}
	if r.State.Combat || r.State.HasEnemies() {
		return bad("Сначала заверши бой")
	}
	index := -1
	for i, quest := range r.State.Quests {
		if quest.ID == proposal.QuestID && quest.Status == "ACTIVE" {
			index = i
			break
		}
	}
	if index < 0 {
		return bad("Эта цель уже неактуальна")
	}
	r.State.Quests[index].Status = "COMPLETED"
	r.State.PendingQuestCompletion = nil
	results := []game.Result{{Type: "UPDATE_QUEST", Status: "COMPLETED", Text: "Цель «" + r.State.Quests[index].Title + "» подтверждена: " + proposal.Reason}}
	if !r.State.Quests[index].Rewarded {
		results = append(results, game.AwardProgress(&r.State, results)...)
		r.State.Quests[index].Rewarded = true
	}
	complete := true
	for _, quest := range r.State.Quests {
		complete = complete && quest.Status == "COMPLETED"
	}
	if complete {
		results = append(results, endCampaign(r, "objective", "Все цели приключения выполнены. Кампания завершена."))
	}
	for _, result := range results {
		if err := s.addEvent(ctx, q, r.ID, user, result.Type, result); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) reopenQuest(ctx context.Context, q *store.Queries, r *Room, user int64, target string) error {
	if !CanManage(r.OwnerID, user) {
		return denied()
	}
	if r.State.Settings.LastLantern() || r.State.Combat {
		return bad("Вернуть эту цель в игру сейчас нельзя")
	}
	for i := range r.State.Quests {
		quest := &r.State.Quests[i]
		if quest.ID != target || quest.Status != "COMPLETED" {
			continue
		}
		quest.Status = "ACTIVE"
		quest.Rewarded = true // Старый финал уже выдал опыт; повторно его не начисляем.
		r.State.PendingQuestCompletion = nil
		return s.addEvent(ctx, q, r.ID, user, "QUEST_REOPENED", game.Result{Type: "QUEST_REOPENED", Text: "Цель «" + quest.Title + "» возвращена в игру по решению владельца."})
	}
	return bad("Завершённая цель не найдена")
}

func settleTurn(r *Room, user int64, wasCombat bool, results []game.Result) ([]game.Result, error) {
	startedCombat := false
	armorBonus := 0
	for _, result := range results {
		if result.Type == "START_COMBAT" {
			startedCombat = true
		}
		if result.Type == "DEFEND" {
			armorBonus = 2
		}
		if result.Type == "CLASS_DEFEND" {
			armorBonus = result.ArmorBonus
		}
	}
	// Also repair old snapshots where enemies in another scene kept combat active.
	if r.State.Combat && !r.State.HasEnemies() {
		r.State.Combat = false
	}
	if startedCombat && r.State.Combat && len(r.State.CombatOrder) == 0 {
		initiative, err := r.State.BeginInitiative(time.Now())
		if err != nil {
			return nil, err
		}
		results = append(results, initiative...)
	} else {
		r.State.EnsureCombatOrder(user, time.Now())
	}
	retaliation, err := game.Retaliate(&r.State, user, armorBonus)
	if err != nil {
		return nil, err
	}
	results = append(results, retaliation...)
	results = append(results, game.AwardProgress(&r.State, results)...)
	if r.State.PartyDefeated() {
		results = append(results, endCampaign(r, "defeat", "Весь отряд пал. Кампания завершена; её историю можно перечитать."))
	} else if (wasCombat || startedCombat) && !r.State.Combat {
		results = append(results, game.Result{Type: "COMBAT_ENDED", Text: "В текущей сцене больше нет враждебных противников. Бой завершён."})
	}
	if r.Status == "PLAYING" && r.State.Settings.LastLantern() && !r.State.Combat && len(r.State.Quests) > 0 && r.State.Quests[0].Status == "COMPLETED" {
		for _, result := range results {
			if result.Type == "UPDATE_QUEST" {
				results = append(results, endCampaign(r, "objective", "Огненный камень установлен, фонарь снова защищает Тихий Брод. Приключение завершено."))
				break
			}
		}
	}
	if r.Status == "PLAYING" {
		for _, result := range results {
			if result.Type == "FINISH_CAMPAIGN" {
				results = append(results, endCampaign(r, "objective", "Все цели приключения выполнены. Кампания завершена."))
				break
			}
		}
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
