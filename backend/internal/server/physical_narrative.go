package server

import (
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var quotedSpeech = regexp.MustCompile(`«[^»]*»|“[^”]*”|"[^"]*"`)

// Strong physical claims are checked before dice against planned effects and
// after dice against the actual snapshot. Dialogue may still contain rumours,
// lies or hypotheticals; it never grants inventory or moves an NPC by itself.
func validatePhysicalNarrative(in ai.Input, actions []game.Action, narrative string) error {
	before := in.State
	if in.BeforeState != nil {
		before = *in.BeforeState
	}
	projected := in.State
	if in.Results == nil {
		projected = in.State.Clone()
		for _, action := range actions {
			switch action.Type {
			case "ATTACK", "SKILL_CHECK", "DICE_ROLL":
				continue
			}
			if _, err := game.Apply(&projected, in.PlayerID, action); err != nil {
				return err
			}
		}
	}
	if in.Intent != nil && before.Scene != nil && projected.Scene != nil && before.Scene.ID != projected.Scene.ID {
		for _, id := range in.Intent.CompanionNPCs {
			npc := projected.NPC(id)
			if npc == nil || !projected.Present(*npc) {
				return fmt.Errorf("спутник %s остался в прежнем месте: после перехода нужен MOVE_NPC name=@current", id)
			}
		}
	}
	hero := in.State.Hero(in.PlayerID)
	if hero == nil {
		return nil
	}
	for _, clause := range strings.FieldsFunc(quotedSpeech.ReplaceAllString(narrative, ""), func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == ';' || r == '\n'
	}) {
		words := attackWords(clause)
		for i, verb := range words {
			if movementNegated(words, i) || hypotheticalEffect(words, i) {
				continue
			}
			actor := physicalActor(before, in.PlayerID, words, i)
			if departureVerb(verb, words) && actor != "" && actor != "@hero" && !reportedMovement(words[:i]) {
				old, moved := before.NPC(actor), projected.NPC(actor)
				if old != nil && moved != nil && old.LocationID == moved.LocationID && old.Location == moved.Location {
					return fmt.Errorf("%s не перемещён: для ухода нужен MOVE_NPC; новое место сначала создай CREATE_LOCATION, отряд остаётся на месте", old.Name)
				}
			}
			transfer := transferVerb(verb)
			gain := actor == "@hero" && gainVerb(verb, clause)
			if transfer && receivesHero(words[i+1:], hero.Name) {
				gain = true
			}
			give := transfer && actor == "@hero" && !gain
			if !gain && !give {
				continue
			}
			key := objectWord(words[i+1:])
			expected := ""
			if in.Intent != nil {
				if in.Intent.ItemRequest != "" {
					expected = in.Intent.ItemRequest
				} else if in.Intent.ItemName != "" {
					expected = objectWord(attackWords(in.Intent.ItemName))
				}
			}
			if expected == "" {
				_, expected, _ = inventoryRequest(in.Text)
			}
			if key == "" || key == "дар" || give && expected != "" {
				key = expected
			}
			if key == "" || abstractItem(key) {
				continue
			}
			if gain && !ownsNamedItem(projected, in.PlayerID, key) {
				return fmt.Errorf("получение вещи «%s» не записано: нужен ADD_ITEM, при проверке — после SKILL_CHECK; без выдачи не описывай передачу или карман героя", key)
			}
			if give && !removedNamedItem(in, actions, key) {
				return fmt.Errorf("передача вещи «%s» не записана: нужен REMOVE_ITEM предмета из инвентаря героя", key)
			}
		}
	}
	return nil
}

func ownsNamedItem(state game.State, user int64, key string) bool {
	if hero := state.Hero(user); hero != nil {
		for _, item := range hero.Inventory {
			if item.Quantity > 0 && hasWorldWord(item.Name, key) {
				return true
			}
		}
	}
	return false
}

func removedNamedItem(in ai.Input, actions []game.Action, key string) bool {
	if in.Results != nil {
		for _, result := range in.Results {
			if result.Type == "REMOVE_ITEM" && result.Item != nil && hasWorldWord(result.Item.Name, key) {
				return true
			}
		}
		return false
	}
	if hero := in.State.Hero(in.PlayerID); hero != nil {
		for _, item := range hero.Inventory {
			for _, action := range actions {
				if action.Type == "REMOVE_ITEM" && action.Target == item.ID && hasWorldWord(item.Name, key) {
					return true
				}
			}
		}
	}
	return false
}

func hypotheticalEffect(words []string, at int) bool {
	for _, word := range words[:at] {
		switch word {
		case "если", "когда", "может", "могла", "мог", "вчера", "раньше":
			return true
		}
	}
	return false
}

func reportedMovement(words []string) bool {
	for _, word := range words {
		switch word {
		case "говорит", "сказал", "утверждает", "видел", "рассказывает", "рассказал", "вспоминает", "слышал":
			return true
		}
	}
	return false
}

func transferVerb(word string) bool {
	switch word {
	case "передает", "передаёт", "передают", "передаете", "передаёте", "передаешь", "передаёшь", "передал", "передала", "передавая", "отдает", "отдаёт", "отдаете", "отдаёте", "отдаешь", "отдаёшь", "отдал", "отдала", "вручает", "вручил", "вручила", "вручаете":
		return true
	}
	return false
}

func gainVerb(word, clause string) bool {
	switch word {
	case "берет", "берёт", "берете", "берёте", "берешь", "берёшь", "взял", "взяла", "взяли", "получает", "получаете", "получаешь", "получил", "получила", "получили", "принимает", "принимаете", "принимаешь", "принял", "приняла", "приняли", "забирает", "забираете", "забираешь", "забрал", "забрала", "подбирает", "подбираете", "подобрал", "подобрала":
		return true
	case "кладет", "кладёт", "кладете", "кладёте", "кладешь", "кладёшь", "положил", "положила", "убирает", "убираете":
		return strings.Contains(clause, "карман") || strings.Contains(clause, "рюкзак") || strings.Contains(clause, "инвентар")
	}
	return false
}

func departureVerb(word string, words []string) bool {
	switch word {
	case "ушел", "ушёл", "ушла", "ушли", "уходит", "уходят", "уехал", "уехала", "уезжает", "покинул", "покинула", "покидает", "сбежал", "сбежала", "убегает":
		return true
	case "исчез", "исчезла", "исчезнуть", "скрылся", "скрылась":
		for _, other := range words {
			if strings.HasPrefix(other, "направля") || strings.HasPrefix(other, "отправля") {
				return true
			}
		}
	}
	return false
}

func receivesHero(words []string, name string) bool {
	for _, word := range words[:min(4, len(words))] {
		if word == "вам" || word == "тебе" || worldWord(word) == worldWord(name) {
			return true
		}
	}
	return false
}

func physicalActor(state game.State, user int64, words []string, at int) string {
	aliases := map[string][]string{}
	for _, npc := range state.NPCs {
		raw := strings.FieldsFunc(npc.Name, func(r rune) bool { return !unicode.IsLetter(r) })
		if len(raw) == 0 {
			continue
		}
		aliases[worldWord(raw[0])] = append(aliases[worldWord(raw[0])], npc.ID)
		if len(raw) > 1 && unicode.IsUpper([]rune(raw[len(raw)-1])[0]) {
			key := worldWord(raw[len(raw)-1])
			aliases[key] = append(aliases[key], npc.ID)
		}
	}
	hero := state.Hero(user)
	for i := at - 1; i >= 0 && i >= at-20; i-- {
		if hero != nil && (words[i] == strings.ToLower(hero.Name) || words[i] == "вы" || words[i] == "ты") {
			return "@hero"
		}
		if ids := aliases[worldWord(words[i])]; len(ids) == 1 {
			return ids[0]
		}
	}
	return ""
}
