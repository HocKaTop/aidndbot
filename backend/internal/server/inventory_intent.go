package server

import (
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

func turnIntent(state game.State, user int64, text string) (ai.PlayerIntent, error) {
	intent := playerIntent(text)
	if intent.SceneChange && !attackCondition(text) {
		for _, clause := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return r == ',' || r == '.' || r == ';' || r == '\n' }) {
			if !playerIntent(clause).SceneChange || !strings.Contains(" "+clause+" ", " с ") {
				continue
			}
			companions := strings.SplitN(clause, " с ", 2)[1]
			words := attackWords(companions)
			for i, word := range words {
				if word == "в" || word == "к" || word == "на" || word == "до" || word == "через" || word == "из" {
					words = words[:i]
					break
				}
			}
			for _, part := range strings.Split(strings.Join(words, " "), " и ") {
				actor := physicalActor(state, user, attackWords(part), len(attackWords(part)))
				if npc := state.NPC(actor); npc != nil {
					if !state.Present(*npc) || !npc.Alive {
						return intent, bad("Спутника «" + npc.Name + "» нет среди живых NPC текущего места. Сначала встреться с ним.")
					}
					intent.CompanionNPCs = append(intent.CompanionNPCs, npc.ID)
				}
			}
		}
	}
	kind, word, phrase := inventoryRequest(text)
	if kind == "" {
		return intent, nil
	}
	hero := state.Hero(user)
	if hero == nil {
		return intent, nil
	}
	for _, npc := range state.NPCs {
		if hasWorldWord(npc.Name, word) {
			return intent, nil // Carrying/touching a person is not inventory acquisition.
		}
	}
	var held *game.Item
	for i := range hero.Inventory {
		item := &hero.Inventory[i]
		if item.Quantity > 0 && hasWorldWord(item.Name, word) {
			if held != nil {
				return intent, bad("Уточни название предмета: в инвентаре несколько подходящих вещей.")
			}
			held = item
		}
	}
	if kind == "give" {
		// A recipient can precede the object ("Передаю Маре цилиндр").
		if held == nil {
			for i := range hero.Inventory {
				item := &hero.Inventory[i]
				key := objectWord(attackWords(item.Name))
				if item.Quantity > 0 && key != "" && hasWorldWord(phrase, key) {
					if held != nil {
						return intent, bad("Укажи один предмет для передачи.")
					}
					held = item
				}
			}
		}
		if held == nil {
			return intent, bad("Названного предмета нет в твоём инвентаре. Сначала получи его; /state покажет твои вещи.")
		}
		intent.GiveItemID, intent.ItemName = held.ID, held.Name
	} else if held != nil && !hasWorldWord(phrase, "второй") && !hasWorldWord(phrase, "другой") && !strings.Contains(phrase, "ещё") && !strings.Contains(phrase, "еще") {
		intent.HeldItemID, intent.ItemName = held.ID, held.Name
	} else {
		intent.ItemRequest = word
	}
	return intent, nil
}

// Only explicit physical requests constrain mechanics. Questions, negations,
// hypotheticals and abstract expressions remain free dialogue.
func inventoryRequest(text string) (kind, word, phrase string) {
	if attackCondition(text) {
		return "", "", ""
	}
	for _, clause := range strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == ';' || r == '\n'
	}) {
		raw := strings.FieldsFunc(clause, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
		words := attackWords(clause)
		for i, verb := range words {
			if i > 0 && words[i-1] != "я" && words[i-1] != "и" && words[i-1] != "затем" && words[i-1] != "потом" {
				continue
			}
			if movementNegated(words, i) {
				continue
			}
			start, requestKind := i+1, ""
			switch verb {
			case "беру", "забираю", "подбираю", "принимаю", "получаю":
				requestKind = "take"
			case "кладу", "убираю":
				if strings.Contains(strings.ToLower(clause), "карман") || strings.Contains(strings.ToLower(clause), "рюкзак") {
					requestKind = "take"
				}
			case "передаю", "отдаю", "дарю":
				requestKind = "give"
			case "прошу":
				for j := start; j < len(words) && j < start+7; j++ {
					if words[j] == "не" {
						break
					}
					if words[j] == "передать" || words[j] == "вернуть" || words[j] == "отдать" || words[j] == "дать" || words[j] == "выдать" || words[j] == "вручить" {
						requestKind, start = "take", j+1
						break
					}
				}
			}
			if requestKind == "" || start >= len(words) {
				continue
			}
			if requestKind == "give" && start+1 < len(raw) && unicode.IsUpper([]rune(raw[start])[0]) && unicode.IsLower([]rune(raw[start+1])[0]) && strings.HasSuffix(words[start], "е") {
				start++ // Named recipient before the object.
			}
			candidate := objectWord(words[start:])
			if candidate == "" || abstractItem(candidate) {
				continue
			}
			return requestKind, candidate, strings.Join(words[start:], " ")
		}
	}
	return "", "", ""
}

func validateInventoryPlan(intent *ai.PlayerIntent, actions []game.Action) error {
	if intent == nil {
		return nil
	}
	count := 0
	for _, action := range actions {
		if action.Type == "ADD_ITEM" && intent.HeldItemID != "" && hasWorldWord(action.Name, objectWord(attackWords(intent.ItemName))) {
			return errors.New("предмет уже у героя: не выдавай повторную копию")
		}
		if intent.ItemRequest != "" && action.Type == "ADD_ITEM" && hasWorldWord(action.Name, intent.ItemRequest) {
			count++
		}
		if intent.GiveItemID != "" && action.Type == "REMOVE_ITEM" && action.Target == intent.GiveItemID {
			count++
		}
	}
	if len(actions) > 0 && (intent.ItemRequest != "" || intent.GiveItemID != "") && count != 1 {
		return fmt.Errorf("получение/передача предмета требует одного ADD_ITEM/REMOVE_ITEM; проверка должна включать это последствие при успехе, либо оставь actions=[] и опиши отказ без передачи")
	}
	return nil
}
