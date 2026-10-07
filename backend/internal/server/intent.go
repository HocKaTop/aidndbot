package server

import (
	"dnd-bot/backend/internal/ai"
	"strings"
	"unicode"
)

// Parse only an explicit request to change the party's location. Searching
// substrings confused lifting an object with climbing and looking for an exit
// with walking through one.
func playerIntent(text string) ai.PlayerIntent {
	intent := ai.PlayerIntent{}
	for _, clause := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r == ',' || r == '.' || r == '!' || r == '?' || r == ';' || r == ':' || r == '\n'
	}) {
		words := strings.FieldsFunc(clause, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
		for i, word := range words {
			if !movementVerb(word) || movementNegated(words, i) {
				continue
			}
			if (word == "иду" || word == "идём" || word == "идем" || word == "пойду" || word == "пойдём" || word == "пойдем" || word == "идти") && i+2 < len(words) && words[i+1] == "к" && localFixture(words[i+2]) {
				continue
			}
			intent.SceneChange = true
			return intent
		}
	}
	return intent
}

func movementVerb(word string) bool {
	switch word {
	case "иду", "идём", "идем", "пойду", "пойдём", "пойдем", "идти", "идёмте", "идемте",
		"вхожу", "входим", "войду", "войдём", "войти", "захожу", "зайду", "зайти",
		"выхожу", "выходим", "выйду", "выйти", "перехожу", "перейду", "перейти",
		"спускаюсь", "спустимся", "спущусь", "спуститься", "поднимаюсь", "поднимусь", "подняться",
		"отправляюсь", "отправимся", "отправиться", "направляюсь", "направляемся", "направиться",
		"возвращаюсь", "возвращаемся", "вернусь", "вернуться", "возвращаться",
		"прохожу", "пройду", "пройти", "бегу", "бежим", "побегу", "добираюсь", "добраться", "дойти", "следую", "следовать":
		return true
	}
	return false
}

func movementNegated(words []string, at int) bool {
	for i := at - 1; i >= 0 && i >= at-3; i-- {
		switch words[i] {
		case "но", "а", "затем", "потом":
			return false
		case "не", "ни", "никогда", "если", "когда", "куда", "как", "зачем", "почему", "можно", "нужно", "надо", "стоит", "ли":
			return true
		}
	}
	return false
}

func localFixture(word string) bool {
	for _, prefix := range []string{"двер", "окн", "парт", "стол", "выход", "лестниц", "шкаф", "сундук"} {
		if strings.HasPrefix(word, prefix) {
			return true
		}
	}
	return false
}
