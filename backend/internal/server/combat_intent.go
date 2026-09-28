package server

import (
	"dnd-bot/backend/internal/game"
	"fmt"
	"strings"
)

func attacksExplicitly(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.TrimPrefix(text, "я ")
	for _, prefix := range []string{"атакую", "атаковать", "нападаю", "напасть", "начать драку", "начинаю драку", "вступить в бой", "вступаю в бой", "сражаюсь", "бью ", "ударить ", "ударяю ", "рублю ", "добить ", "добиваю "} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func combatNarrative(results []game.Result) string {
	lines := []string{}
	for _, result := range results {
		switch result.Type {
		case "START_COMBAT":
			lines = append(lines, "Бой начинается.")
		case "ATTACK":
			if result.Attack == nil {
				continue
			}
			a := result.Attack
			switch {
			case !a.Hit:
				lines = append(lines, fmt.Sprintf("Атака по %s не попала.", a.Target))
			case a.TargetHP == 0:
				lines = append(lines, fmt.Sprintf("Удар наносит %d урона. %s повержен.", a.Damage, a.Target))
			default:
				lines = append(lines, fmt.Sprintf("Удар по %s наносит %d урона; у противника остаётся %d HP.", a.Target, a.Damage, a.TargetHP))
			}
		case "NPC_ATTACK", "COMBAT_ENDED", "GAME_FINISHED":
			lines = append(lines, result.Text)
		}
	}
	return strings.Join(lines, " ")
}

func ensureLanternThief(state *game.State, user int64) (*game.Result, error) {
	if !state.Settings.LastLantern() || state.Scene == nil || !strings.Contains(strings.ToLower(state.Scene.Title+" "+state.Scene.Location), "мельн") {
		return nil, nil
	}
	for _, n := range state.NPCs {
		if strings.Contains(strings.ToLower(n.Name), "похит") {
			return nil, nil
		}
	}
	result, err := game.Apply(state, user, game.Action{Type: "CREATE_NPC", Name: "Похититель", Description: "Напуганный хранитель огненного камня.", Status: "neutral"})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// An explicit attack is a mechanical command. Never let the narrator describe
// combat without changing the authoritative combat state and rolling dice.
func applyExplicitAttack(state *game.State, user int64, text string) ([]game.Result, bool, error) {
	if !attacksExplicitly(text) {
		return nil, false, nil
	}
	results := []game.Result{}
	var candidates []*game.NPC
	matchedName := false
	for i := range state.NPCs {
		n := &state.NPCs[i]
		if !n.Alive || !state.Present(*n) {
			continue
		}
		name := strings.ToLower(n.Name)
		if strings.Contains(strings.ToLower(text), name) || (len(name) >= 6 && strings.Contains(strings.ToLower(text), strings.TrimSuffix(name, "ь"))) {
			candidates = []*game.NPC{n}
			matchedName = true
			break
		}
		if n.Disposition != "friendly" {
			candidates = append(candidates, n)
		}
	}
	if !matchedName && state.Settings.LastLantern() && state.Scene != nil && strings.Contains(strings.ToLower(state.Scene.Title+" "+state.Scene.Location), "мельн") {
		thiefHere := false
		for _, n := range state.NPCs {
			if strings.Contains(strings.ToLower(n.Name), "похит") {
				thiefHere = true
				break
			}
		}
		if !thiefHere {
			created, err := ensureLanternThief(state, user)
			if err != nil {
				return nil, true, err
			}
			if created == nil {
				return nil, true, bad("Похититель уже побеждён или находится в другой сцене.")
			}
			results = append(results, *created)
			candidates = []*game.NPC{&state.NPCs[len(state.NPCs)-1]}
		}
	}
	if len(candidates) != 1 {
		return nil, true, bad("Укажи имя противника из текущей сцены. Если его здесь нет, сначала найди его.")
	}
	target := candidates[0]
	var actions []game.Action
	if target.Disposition != "hostile" {
		actions = append(actions, game.Action{Type: "SET_DISPOSITION", Target: target.ID, Status: "hostile"})
	}
	if !state.Combat {
		actions = append(actions, game.Action{Type: "START_COMBAT"})
	}
	actions = append(actions, game.Action{Type: "ATTACK", Target: target.ID})
	attack, err := game.ApplyActions(state, user, actions)
	if err != nil {
		return nil, true, err
	}
	return append(results, attack...), true, nil
}
