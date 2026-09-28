package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"errors"
	"strings"
)

func lobbyReady(room *Room) error {
	if len(room.Members) == 0 {
		return bad("В отряде пока нет игроков")
	}
	var waiting []string
	for _, member := range room.Members {
		hero := room.State.Hero(member.UserID)
		if hero == nil || hero.HP <= 0 {
			waiting = append(waiting, member.Name+" — нужен живой персонаж")
		} else if !member.Ready {
			waiting = append(waiting, member.Name+" — нажми «Я готов» или отправь /ready")
		}
	}
	if len(waiting) > 0 {
		return bad("Ждём отряд: " + strings.Join(waiting, "; "))
	}
	return nil
}

func validateOpening(actions []game.Action) error {
	if len(actions) < 2 || len(actions) > 4 || actions[0].Type != "MOVE_SCENE" {
		return errors.New("для вступления нужны сначала одна сцена MOVE_SCENE и одна цель CREATE_QUEST")
	}
	quests := 0
	if strings.TrimSpace(actions[0].Description) == "" {
		return errors.New("начальная сцена должна содержать описание ситуации")
	}
	for _, action := range actions[1:] {
		switch action.Type {
		case "CREATE_QUEST":
			quests++
			if strings.TrimSpace(action.Description) == "" {
				return errors.New("первая цель должна содержать описание задачи для отряда")
			}
		case "CREATE_NPC":
			if action.Status != "friendly" && action.Status != "neutral" {
				return errors.New("во вступлении NPC должен быть friendly или neutral")
			}
		default:
			return errors.New("во вступлении после сцены допустимы только CREATE_QUEST и мирные CREATE_NPC")
		}
	}
	if quests != 1 {
		return errors.New("во вступлении нужна ровно одна цель CREATE_QUEST")
	}
	return nil
}

// Called inside the start transaction: a failed opening leaves the lobby intact.
func (s *Server) openAdventure(ctx context.Context, q *store.Queries, room *Room, user int64) error {
	out, err := s.planTurn(ctx, room.ID, ai.Input{
		Opening: true, State: room.State, PlayerID: user,
		Text: "Открой приключение для всего отряда: вступление, начальная сцена и первая цель.",
	})
	if err != nil {
		return err
	}
	results, err := game.ApplyActions(&room.State, user, out.Actions)
	if err != nil {
		return err
	}
	rememberGMNotes(&room.State, out.Memory)
	room.Status = "PLAYING"
	if err = s.addEvent(ctx, q, room.ID, user, "GAME_STARTED", game.Result{Type: "GAME_STARTED", Text: "Отряд готов. Приключение начинается."}); err != nil {
		return err
	}
	for _, result := range results {
		room.State.Summary += "\nНачало: " + result.Text
		if err = s.addEvent(ctx, q, room.ID, 0, result.Type, result); err != nil {
			return err
		}
	}
	return s.addEvent(ctx, q, room.ID, 0, "GM_MESSAGE", game.Result{Type: "GM_MESSAGE", Text: out.Narrative})
}
