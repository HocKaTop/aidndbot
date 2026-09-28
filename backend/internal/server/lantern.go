package server

import (
	"context"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"strings"
)

// The short tutorial has concrete, deterministic ways to take and install its
// key item. Both use the same transaction, turn counter and event log as AI turns.
func (s *Server) lanternCommand(ctx context.Context, q *store.Queries, r *Room, user int64, kind string) error {
	if !r.State.Settings.LastLantern() || r.State.Scene == nil || len(r.State.Quests) == 0 {
		return bad("Это действие доступно только в приключении «Последний фонарь».")
	}
	hero := r.State.Hero(user)
	if hero == nil || hero.HP <= 0 {
		return bad("Нужен живой персонаж.")
	}
	var actions []game.Action
	text := ""
	switch kind {
	case "return_to_bridge":
		if r.State.Combat || r.State.HasEnemies() {
			return bad("Сначала заверши бой.")
		}
		place := strings.ToLower(r.State.Scene.Title + " " + r.State.Scene.Location)
		if strings.Contains(place, "мост") || strings.Contains(place, "фонар") {
			return bad("Отряд уже у фонаря. Теперь установи камень.")
		}
		stone := false
		for _, item := range hero.Inventory {
			stone = stone || (item.Quantity > 0 && strings.EqualFold(strings.TrimSpace(item.Name), "Огненный камень"))
		}
		if !stone {
			return bad("Для возвращения к мосту сначала добудь огненный камень.")
		}
		actions = []game.Action{{Type: "MOVE_SCENE", Name: "Мост Тихого Брода", Description: "Погасший фонарь ждёт огненный камень."}}
		text = hero.Name + " возвращается с огненным камнем к мосту Тихого Брода."
	case "claim_stone":
		if !strings.Contains(strings.ToLower(r.State.Scene.Title+" "+r.State.Scene.Location), "мельн") {
			return bad("Огненный камень можно забрать у похитителя на мельнице.")
		}
		available := false
		for _, n := range r.State.NPCs {
			if strings.Contains(strings.ToLower(n.Name), "похит") && r.State.Present(n) && (!n.Alive || n.Disposition == "friendly") {
				available = true
			}
		}
		if !available {
			return bad("Похититель ещё не отдал камень. Договорись с ним или победи его в бою.")
		}
		actions = []game.Action{{Type: "ADD_ITEM", Name: "Огненный камень", Description: "Камень для фонаря у моста."}}
		text = hero.Name + " забирает огненный камень у похитителя."
	case "install_stone":
		if r.State.Combat || r.State.HasEnemies() {
			return bad("Сначала заверши бой.")
		}
		stone := ""
		for _, item := range hero.Inventory {
			if item.Quantity > 0 && strings.EqualFold(strings.TrimSpace(item.Name), "Огненный камень") {
				stone = item.ID
				break
			}
		}
		if stone == "" {
			return bad("У твоего героя нет огненного камня. Проверь инвентарь.")
		}
		actions = []game.Action{{Type: "REMOVE_ITEM", Target: stone}, {Type: "UPDATE_QUEST", Target: r.State.Quests[0].ID, Status: "COMPLETED"}}
		text = hero.Name + " устанавливает огненный камень в фонарь у моста."
	default:
		return bad("Неизвестное действие.")
	}
	wasCombat := r.State.Combat
	results, err := game.ApplyActions(&r.State, user, actions)
	if err != nil {
		return bad(err.Error())
	}
	results, err = settleTurn(r, user, wasCombat, results)
	if err != nil {
		return err
	}
	if err := s.addEvent(ctx, q, r.ID, user, "PLAYER_ACTION", game.Result{Type: "PLAYER_ACTION", Text: text}); err != nil {
		return err
	}
	for _, result := range results {
		if err := s.addEvent(ctx, q, r.ID, user, result.Type, result); err != nil {
			return err
		}
	}
	rememberTurn(&r.State, results)
	return nil
}
