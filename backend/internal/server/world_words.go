package server

import (
	"strconv"
	"strings"
)

// Compare inflected references by whole words, rather than by substrings:
// цилиндр/цилиндра match, while камень/кампания and страж/стражник do not.
func worldWord(word string) string {
	word = strings.ReplaceAll(strings.ToLower(word), "ё", "е")
	for _, ending := range []string{"ами", "ями", "ого", "ему", "ому", "ой", "ая", "ую", "ое", "ые", "ий", "ый", "ом", "ем", "а", "я", "ы", "и", "е", "у", "ю", "о", "ь", "й"} {
		if strings.HasSuffix(word, ending) && len([]rune(strings.TrimSuffix(word, ending))) >= 2 {
			return strings.TrimSuffix(word, ending)
		}
	}
	return word
}

func hasWorldWord(text, word string) bool {
	want := worldWord(word)
	for _, candidate := range attackWords(text) {
		if worldWord(candidate) == want {
			return true
		}
	}
	return false
}

func abstractItem(word string) bool {
	switch worldWord(word) {
	case "памят", "слов", "ответ", "совет", "помощ", "прощени", "согласи", "решени", "ответственност", "инициатив", "контрол", "сообщени", "информаци", "объяснени", "внимани", "показани", "довери", "безопасност", "шанс", "возможност", "опыт", "уровен", "благословени", "передышк", "пауз", "себ", "ребенк", "ребенок", "геро", "рук", "ладон", "дыхан", "ресурс", "предложени", "управлени", "девушк", "девочк", "человек", "мальчик":
		return true
	}
	return false
}

func objectWord(words []string) string {
	for _, word := range words {
		if _, err := strconv.Atoi(word); err == nil {
			continue
		}
		switch word {
		case "его", "ее", "её", "их", "это", "этот", "эту", "тот", "мой", "свой", "ваш", "твой", "бережно", "осторожно", "сразу", "обратно", "себе", "мне", "нам", "вам", "тебе", "вот", "в", "из", "на", "к", "с":
			continue
		}
		adjective := false
		for _, ending := range []string{"ого", "ему", "ому", "ой", "ая", "ую", "ое", "ые", "ий", "ый"} {
			adjective = adjective || strings.HasSuffix(word, ending)
		}
		if !adjective {
			return word
		}
	}
	return ""
}
