package server

import (
	"context"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"strings"
)

// The short tutorial has concrete, deterministic ways to take and install its
// key item. Both use the same transaction, turn counter and event log as AI turns.
func lanternActions(state *game.State, user int64, kind string) ([]game.Action, string, error) {
	if !state.Settings.LastLantern() || state.Scene == nil || len(state.Quests) == 0 {
		return nil, "", bad("Это действие доступно только в приключении «Последний фонарь».")
	}
	hero := state.Hero(user)
	if hero == nil || hero.HP <= 0 {
		return nil, "", bad("Нужен живой персонаж.")
	}
	var actions []game.Action
	text := ""
	switch kind {
	case "return_to_bridge":
		if state.Combat || state.HasEnemies() {
			return nil, "", bad("Сначала заверши бой.")
		}
		place := strings.ToLower(state.Scene.Title + " " + state.Scene.Location)
		if strings.Contains(place, "мост") || strings.Contains(place, "фонар") {
			return nil, "", bad("Отряд уже у фонаря. Теперь установи камень.")
		}
		stone := false
		for _, item := range hero.Inventory {
			stone = stone || (item.Quantity > 0 && strings.EqualFold(strings.TrimSpace(item.Name), "Огненный камень"))
		}
		if !stone {
			return nil, "", bad("Для возвращения к мосту сначала добудь огненный камень.")
		}
		actions = []game.Action{{Type: "MOVE_SCENE", Name: "Мост Тихого Брода", Description: "Погасший фонарь ждёт огненный камень."}}
		for _, known := range state.Locations {
			if strings.Contains(strings.ToLower(known.Title+" "+known.Location), "мост") {
				actions = []game.Action{{Type: "REVISIT_SCENE", Target: known.ID}}
				break
			}
		}
		text = hero.Name + " возвращается с огненным камнем к мосту Тихого Брода."
	case "claim_stone":
		if !strings.Contains(strings.ToLower(state.Scene.Title+" "+state.Scene.Location), "мельн") {
			return nil, "", bad("Огненный камень можно забрать у похитителя на мельнице.")
		}
		available := false
		for _, n := range state.NPCs {
			if strings.Contains(strings.ToLower(n.Name), "похит") && state.Present(n) && (!n.Alive || n.Disposition == "friendly") {
				available = true
			}
		}
		if !available {
			return nil, "", bad("Похититель ещё не отдал камень. Договорись с ним или победи его в бою.")
		}
		actions = []game.Action{{Type: "ADD_ITEM", Name: "Огненный камень", Description: "Камень для фонаря у моста."}}
		text = hero.Name + " забирает огненный камень у похитителя."
	case "install_stone":
		if state.Combat || state.HasEnemies() {
			return nil, "", bad("Сначала заверши бой.")
		}
		stone := ""
		for _, item := range hero.Inventory {
			if item.Quantity > 0 && strings.EqualFold(strings.TrimSpace(item.Name), "Огненный камень") {
				stone = item.ID
				break
			}
		}
		if stone == "" {
			return nil, "", bad("У твоего героя нет огненного камня. Проверь инвентарь.")
		}
		actions = []game.Action{{Type: "REMOVE_ITEM", Target: stone}, {Type: "UPDATE_QUEST", Target: state.Quests[0].ID, Status: "COMPLETED"}}
		text = hero.Name + " устанавливает огненный камень в фонарь у моста."
	default:
		return nil, "", bad("Неизвестное действие.")
	}
	return actions, text, nil
}

func (s *Server) lanternCommand(ctx context.Context, q *store.Queries, r *Room, user int64, kind string) error {
	actions, text, err := lanternActions(&r.State, user, kind)
	if err != nil {
		return err
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
