package server

import (
	"dnd-bot/backend/internal/game"
	"slices"
	"strings"
)

// The tutorial's explicit item requests use the same engine operations as its
// buttons. Other settings and ambiguous requests still go to the GM.
func applyLanternIntent(state *game.State, user int64, text string) ([]game.Result, bool, error) {
	if !state.Settings.LastLantern() || state.Scene == nil {
		return nil, false, nil
	}
	kind := explicitLanternKind(text)
	if kind == "" {
		return nil, false, nil
	}
	var before []game.Result
	var actions []game.Action
	if kind == "negotiate_stone" {
		if !strings.Contains(strings.ToLower(state.Scene.Title+" "+state.Scene.Location), "мельн") {
			return nil, false, nil
		}
		if state.Combat || state.HasEnemies() {
			return nil, false, nil
		}
		for _, hero := range state.Characters {
			for _, item := range hero.Inventory {
				if item.Quantity > 0 && strings.EqualFold(strings.TrimSpace(item.Name), "Огненный камень") {
					return []game.Result{{Type: "ITEM_ALREADY_HELD", Text: "Огненный камень уже у героя " + hero.Name + "."}}, true, nil
				}
			}
		}
		appeared, err := ensureLanternThief(state, user)
		if err != nil {
			return nil, true, err
		}
		if appeared != nil {
			before = append(before, *appeared)
		}
		for _, npc := range state.NPCs {
			if !state.Present(npc) || !strings.Contains(strings.ToLower(npc.Name), "похит") {
				continue
			}
			if npc.Alive && npc.Disposition != "friendly" {
				actions = []game.Action{{Type: "SKILL_CHECK", Skill: "charisma", DC: 12}, {Type: "SET_DISPOSITION", Target: npc.ID, Status: "friendly"}}
			}
			actions = append(actions, game.Action{Type: "ADD_ITEM", Name: "Огненный камень", Description: "Камень для фонаря у моста."})
			break
		}
		if len(actions) == 0 {
			return nil, false, nil
		}
	} else {
		var err error
		actions, _, err = lanternActions(state, user, kind)
		if err != nil {
			return nil, true, err
		}
	}
	results, err := game.ApplyActions(state, user, actions)
	if err != nil {
		return nil, true, bad(err.Error())
	}
	return append(before, results...), true, nil
}

func explicitLanternKind(text string) string {
	if attackCondition(text) {
		return ""
	}
	wantsStone := mentionsLanternStone(text)
	for _, clause := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r == ',' || r == '.' || r == '!' || r == '?' || r == ';' || r == ':' || r == '\n'
	}) {
		words := attackWords(clause)
		if len(words) > 0 && words[0] == "я" {
			words = words[1:]
		}
		if len(words) == 0 || attackCondition(clause) {
			continue
		}
		if mentionsLanternStone(clause) && words[0] == "устанавливаю" && !slices.Contains(words, "не") && strings.Contains(clause, "фонар") {
			return "install_stone"
		}
		if wantsStone && len(words) > 1 && words[0] == "прошу" {
			switch words[1] {
			case "передать", "вернуть", "отдать":
				return "negotiate_stone"
			}
		}
		if strings.Contains(clause, "мост") && playerIntent(clause).SceneChange {
			for i, word := range words {
				switch word {
				case "возвращаюсь", "возвращаемся", "вернусь", "вернуться":
					if !movementNegated(words, i) {
						return "return_to_bridge"
					}
				}
			}
		}
	}
	return ""
}

func mentionsLanternStone(text string) bool {
	for _, word := range attackWords(text) {
		switch word {
		case "камень", "камня", "камню", "камнем", "камне":
			return true
		}
	}
	return false
}
