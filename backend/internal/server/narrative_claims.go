package server

import (
	"dnd-bot/backend/internal/game"
	"errors"
	"regexp"
	"strings"
)

var (
	locationClaim   = regexp.MustCompile(`(?i)(?:ты|вы|отряд|герои)\s+(?:(?:уже\s+)?(?:оказал[а-яё]*|попал[а-яё]*|прибы[а-яё]*|вош[а-яё]*|заш[а-яё]*|добрал[а-яё]*|добира[а-яё]*|дош[а-яё]*)\s+|уже\s+)(?:в|на|до|к)\s+([\p{L}-]+(?:[ \t]+[\p{L}-]+){0,3})`)
	inventoryAfter  = regexp.MustCompile(`(?i)([\p{L}-]+)\s+(?:лежит|находится|оказал[а-я]*)\s+в\s+(?:тво[её]м|вашем)\s+инвентаре`)
	inventoryBefore = regexp.MustCompile(`(?i)в\s+(?:тво[её]м|вашем)\s+инвентаре\s+(?:появил[а-я]*|лежит|находится)\s+([\p{L}-]+)`)
	combatClaim     = regexp.MustCompile(`(?i)(?:бой|сражение|схватка)\s+(?:начал[а-яё]*|начин[а-яё]*|завязал[а-яё]*)|(?:ты|вы|отряд)\s+вступил[а-яё]*\s+в\s+бой`)
)

// These focused prose checks complement structured actions and engine results.
// They cover strong claims about changing place, death, inventory and combat;
// ordinary atmosphere and dialogue remain free-form.
func validateWorldClaims(state game.State, user int64, actions []game.Action, results []game.Result, narrative string) error {
	plan := results == nil
	changedScene, startedCombat := false, state.Combat
	if plan {
		for i, a := range actions {
			// Consequences after a check are not confirmed until the dice resolve.
			if i > 0 && len(actions) > 0 && actions[0].Type == "SKILL_CHECK" {
				continue
			}
			changedScene = changedScene || a.Type == "MOVE_SCENE" || a.Type == "REVISIT_SCENE"
			startedCombat = startedCombat || a.Type == "START_COMBAT"
		}
	} else {
		for _, r := range results {
			changedScene = changedScene || r.Type == "MOVE_SCENE" || r.Type == "REVISIT_SCENE"
			startedCombat = startedCombat || r.Type == "START_COMBAT"
		}
	}
	for _, match := range locationClaim.FindAllStringSubmatch(narrative, -1) {
		place := narratedPlaceNoun(match[1])
		if place == "" {
			continue
		}
		if plan && changedScene && !plannedPlaceMatches(state, actions, place) {
			return errors.New("описанное место не совпадает с предложенным переходом")
		}
		if !changedScene && !currentPlaceMatches(state.Scene, place) {
			return errors.New("повествование переместило отряд без подтверждённого перехода")
		}
		if !plan && !currentPlaceMatches(state.Scene, place) {
			return errors.New("описанное место не совпадает с текущей локацией")
		}
	}
	for _, npc := range state.NPCs {
		if npc.Alive && claimsNPCDeath(narrative, npc.Name) {
			return errors.New("NPC объявлен мёртвым без результата движка")
		}
	}
	for _, pattern := range []*regexp.Regexp{inventoryAfter, inventoryBefore} {
		for _, match := range pattern.FindAllStringSubmatch(narrative, -1) {
			if !hasNarratedItem(state, user, actions, plan, match[1]) {
				return errors.New("предмет объявлен в инвентаре без подтверждённого получения")
			}
		}
	}
	if combatClaim.MatchString(narrative) && !startedCombat {
		return errors.New("повествование начало бой без результата движка")
	}
	return nil
}

func narratedPlaceNoun(phrase string) string {
	for _, word := range strings.Fields(strings.ToLower(phrase)) {
		switch word {
		case "из", "у", "в", "на", "к", "до", "с", "и", "что":
			return ""
		}
		if locationNoun(word) {
			return word
		}
	}
	return ""
}

func locationNoun(word string) bool {
	for _, stem := range []string{"подвал", "таверн", "комнат", "класс", "коридор", "мельниц", "пещер", "башн", "мост", "деревн", "станц", "чердак", "крыш", "кают", "тоннел", "туннел"} {
		if strings.HasPrefix(word, stem) {
			return true
		}
	}
	return false
}

func currentPlaceMatches(scene *game.Scene, word string) bool {
	if scene == nil {
		return false
	}
	return placeTextMatches(scene.Title+" "+scene.Location, word)
}

func plannedPlaceMatches(state game.State, actions []game.Action, word string) bool {
	for _, action := range actions {
		switch action.Type {
		case "MOVE_SCENE":
			if placeTextMatches(action.Name, word) {
				return true
			}
		case "REVISIT_SCENE":
			if currentPlaceMatches(state.Location(action.Target), word) {
				return true
			}
		}
	}
	return false
}

func placeTextMatches(text, word string) bool {
	runes := []rune(word)
	stem := word
	if len(runes) > 4 {
		stem = string(runes[:len(runes)-1])
	}
	return strings.Contains(strings.ToLower(text), stem)
}

func claimsNPCDeath(narrative, name string) bool {
	for _, clause := range strings.FieldsFunc(narrative, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == ';' || r == ':' || r == '\n'
	}) {
		words := attackWords(clause)
		for i, word := range words {
			if !mentionsNPC(word, name) {
				continue
			}
			for j := max(0, i-2); j <= min(len(words)-1, i+2); j++ {
				if j == i || !deathWord(words[j]) || j > 0 && (words[j-1] == "не" || words[j-1] == "будет") {
					continue
				}
				bridge := words[min(i, j)+1 : max(i, j)]
				near := true
				for _, between := range bridge {
					if between != "уже" && between != "теперь" && between != "был" && between != "была" && between != "оказался" && between != "оказалась" && between != "лежит" && between != "лежал" {
						near = false
					}
				}
				if near {
					return true
				}
			}
		}
	}
	return false
}

func deathWord(word string) bool {
	return strings.HasPrefix(word, "мёртв") || strings.HasPrefix(word, "мертв") || strings.HasPrefix(word, "погиб") || strings.HasPrefix(word, "убит") || strings.HasPrefix(word, "повержен")
}

func hasNarratedItem(state game.State, user int64, actions []game.Action, plan bool, word string) bool {
	word = strings.ToLower(word)
	runes := []rune(word)
	stem := word
	if len(runes) > 4 {
		stem = string(runes[:len(runes)-1])
	}
	if hero := state.Hero(user); hero != nil {
		for _, item := range hero.Inventory {
			if item.Quantity > 0 && strings.Contains(strings.ToLower(item.Name), stem) {
				return true
			}
		}
	}
	if plan {
		for i, action := range actions {
			if i > 0 && actions[0].Type == "SKILL_CHECK" {
				break
			}
			if action.Type == "ADD_ITEM" && strings.Contains(strings.ToLower(action.Name), stem) {
				return true
			}
		}
	}
	return false
}
