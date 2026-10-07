package server

import (
	"dnd-bot/backend/internal/game"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

func attacksExplicitly(text string) bool {
	_, ok := attackTail(text)
	return ok
}

func attackTail(text string) (string, bool) {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.TrimPrefix(text, "я ")
	if attackAbility(text) != "" {
		for _, prefix := range []string{"использую", "применяю", "кастую"} {
			if strings.HasPrefix(text, prefix+" ") {
				return strings.TrimSpace(strings.TrimPrefix(text, prefix)), true
			}
		}
	}
	for _, prefix := range []string{"атакую", "атаковать", "нападаю", "напасть", "начать драку", "начинаю драку", "вступить в бой", "вступаю в бой", "сражаюсь", "бью", "ударить", "ударяю", "рублю", "добить", "добиваю"} {
		if text == prefix {
			return "", true
		}
		if strings.HasPrefix(text, prefix+" ") {
			return strings.TrimSpace(strings.TrimPrefix(text, prefix)), true
		}
	}
	return "", false
}

func attackWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

// Match a small set of case endings on the final name word. Prefix matching
// would confuse, for example, "страж" with "стражник".
func mentionsNPC(text, name string) bool {
	nameWords := attackWords(name)
	if len(nameWords) == 0 {
		return false
	}
	noun := nameWords[len(nameWords)-1]
	forms := []string{noun}
	runes := []rune(noun)
	if len(runes) >= 3 {
		base := string(runes[:len(runes)-1])
		switch runes[len(runes)-1] {
		case 'ь':
			forms = append(forms, base+"я", base+"ю", base+"ем", base+"е", base+"и")
		case 'й':
			forms = append(forms, base+"я", base+"ю", base+"ем", base+"е")
		case 'а':
			forms = append(forms, base+"у", base+"ы", base+"е", base+"ой")
		case 'я':
			forms = append(forms, base+"ю", base+"и", base+"е", base+"ей")
		default:
			forms = append(forms, noun+"а", noun+"у", noun+"ом", noun+"е", noun+"ы")
		}
	}
	for _, word := range attackWords(text) {
		for _, form := range forms {
			if word == form {
				return true
			}
		}
	}
	return false
}

func attackCondition(text string) bool {
	for _, word := range attackWords(text) {
		if word == "если" || word == "когда" || word == "пока" {
			return true
		}
	}
	return strings.Contains(strings.ToLower(text), "при условии")
}

func attackAbility(text string) string {
	text = strings.ToLower(text)
	switch {
	case strings.Contains(text, "мощн") && strings.Contains(text, "удар"):
		return "power_strike"
	case strings.Contains(text, "точн") && strings.Contains(text, "удар"):
		return "precise_strike"
	case strings.Contains(text, "огненн") && strings.Contains(text, "снаряд"), strings.Contains(text, "firebolt"), strings.Contains(text, "фаерболт"):
		return "firebolt"
	}
	return ""
}

func simpleAttackTail(tail string, target *game.NPC, ability string) bool {
	words := attackWords(tail)
	if len(words) == 0 || (len(words) == 1 && (words[0] == "его" || words[0] == "её" || words[0] == "ее" || words[0] == "их")) {
		return ability == ""
	}
	if target == nil {
		return false
	}
	if ability != "" {
		filtered := words[:0]
		for _, word := range words {
			if word == "с" || word == "помощью" || word == "используя" || word == "по" || word == "на" || word == "против" || word == "в" || strings.HasPrefix(word, "мощн") || strings.HasPrefix(word, "точн") || strings.HasPrefix(word, "удар") || strings.HasPrefix(word, "огненн") || strings.HasPrefix(word, "снаряд") || word == "firebolt" || word == "фаерболт" {
				continue
			}
			filtered = append(filtered, word)
		}
		words = filtered
		if len(words) == 0 {
			return true
		}
	}
	return len(words) == 1 && mentionsNPC(words[0], target.Name)
}

type parsedAttack struct {
	targetID    string
	createThief bool
	conditional bool
	ability     string
	simple      bool
}

func parseAttack(state *game.State, text string) (parsedAttack, bool, error) {
	tail, ok := attackTail(text)
	if !ok {
		return parsedAttack{}, false, nil
	}
	intent := parsedAttack{conditional: attackCondition(text), ability: attackAbility(text)}
	targetText := tail
	for _, separator := range []string{",", " если ", " когда ", " пока ", " при условии "} {
		if index := strings.Index(targetText, separator); index >= 0 {
			targetText = targetText[:index]
		}
	}
	for _, prefix := range []string{"если ", "когда ", "пока ", "при условии "} {
		if strings.HasPrefix(targetText, prefix) {
			targetText = ""
			break
		}
	}
	var matched []*game.NPC
	var local []*game.NPC
	for i := range state.NPCs {
		n := &state.NPCs[i]
		if mentionsNPC(targetText, n.Name) {
			matched = append(matched, n)
		}
		if n.Alive && state.Present(*n) && n.Disposition != "friendly" {
			local = append(local, n)
		}
	}
	if len(matched) > 1 {
		return intent, true, bad("Найдено несколько NPC с похожим именем. Уточни цель атаки.")
	}
	if len(matched) == 1 {
		n := matched[0]
		if !n.Alive || !state.Present(*n) {
			return intent, true, bad("Названной цели нет среди живых NPC текущей сцены.")
		}
		intent.targetID = n.ID
		intent.simple = simpleAttackTail(tail, n, intent.ability)
		return intent, true, nil
	}
	words := attackWords(targetText)
	generic := len(words) == 0 || (len(words) == 1 && (words[0] == "его" || words[0] == "её" || words[0] == "ее" || words[0] == "их"))
	if state.Settings.LastLantern() && state.Scene != nil && strings.Contains(strings.ToLower(state.Scene.Title+" "+state.Scene.Location), "мельн") {
		thiefNamed := false
		for _, word := range words {
			thiefNamed = thiefNamed || strings.HasPrefix(word, "похитител")
		}
		thiefExists := false
		for _, n := range state.NPCs {
			thiefExists = thiefExists || strings.Contains(strings.ToLower(n.Name), "похит")
		}
		if !thiefExists && (thiefNamed || generic && len(local) == 0) {
			intent.createThief = true
			intent.simple = !intent.conditional && (generic || thiefNamed && len(words) == 1 && intent.ability == "")
			return intent, true, nil
		}
	}
	if !generic {
		if len(local) == 1 && intent.ability != "" && !intent.conditional && simpleAttackTail(targetText, local[0], intent.ability) {
			intent.targetID = local[0].ID
			intent.simple = true
			return intent, true, nil
		}
		return intent, true, bad("Названной цели нет в текущей сцене. Укажи имя доступного NPC или сначала найди её.")
	}
	if len(local) != 1 {
		return intent, true, bad("Укажи имя противника из текущей сцены. Если его здесь нет, сначала найди его.")
	}
	intent.targetID = local[0].ID
	intent.simple = !intent.conditional && intent.ability == ""
	return intent, true, nil
}

func validateAttackPlan(state *game.State, text string, actions []game.Action) error {
	intent, recognized, err := parseAttack(state, text)
	if err != nil || !recognized {
		return err
	}
	lower := strings.ToLower(text)
	magicMethod := intent.ability != "" || strings.Contains(lower, "заклинан") || strings.Contains(lower, "магией") || strings.Contains(lower, "магическ") || strings.Contains(lower, "фаербол") || strings.Contains(lower, "молни") || strings.Contains(lower, "огненн") && strings.Contains(lower, "шар") || strings.Contains(lower, "ледян") && strings.Contains(lower, "стрел")
	for _, action := range actions {
		combatAction := action.Type == "ATTACK" || action.Type == "START_COMBAT" || action.Type == "SET_DISPOSITION" && action.Status == "hostile"
		if intent.conditional && combatAction {
			return errors.New("условная атака не выполняется заранее; опиши условие и попроси игрока подтвердить удар, когда оно наступит")
		}
		if intent.targetID != "" && (action.Type == "ATTACK" || action.Type == "SET_DISPOSITION" && action.Status == "hostile") && action.Target != intent.targetID {
			return errors.New("цель атаки не совпадает с названным игроком NPC")
		}
		if magicMethod && combatAction {
			return errors.New("названное заклинание или классовый приём нельзя заменять обычным боевым действием")
		}
	}
	return nil
}

func combatNarrative(results []game.Result) string {
	lines := []string{}
	for _, result := range results {
		switch result.Type {
		case "START_COMBAT":
			lines = append(lines, "Бой начинается.")
		case "CLASS_ATTACK":
			lines = append(lines, result.Text)
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
	intent, recognized, err := parseAttack(state, text)
	if err != nil || !recognized {
		return nil, recognized, err
	}
	if intent.conditional || !intent.simple {
		return nil, false, nil
	}
	results := []game.Result{}
	if intent.createThief {
		created, err := ensureLanternThief(state, user)
		if err != nil {
			return nil, true, err
		}
		if created == nil {
			return nil, true, bad("Похититель уже побеждён или находится в другой сцене.")
		}
		results = append(results, *created)
		intent.targetID = state.NPCs[len(state.NPCs)-1].ID
	}
	target := state.NPC(intent.targetID)
	if target == nil {
		return nil, true, bad("Цель атаки не найдена.")
	}
	if intent.ability != "" {
		abilityResults, err := game.UseAbility(state, user, intent.ability, target.ID)
		if err != nil {
			return nil, true, bad(err.Error())
		}
		return append(results, abilityResults...), true, nil
	}
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
