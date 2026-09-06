package game

import "errors"

func Modifier(score int) int {
	if score < 10 {
		return (score - 11) / 2
	}
	return (score - 10) / 2
}
func SkillModifier(s Stats, skill string) (int, error) {
	switch skill {
	case "strength":
		return Modifier(s.Strength), nil
	case "dexterity":
		return Modifier(s.Dexterity), nil
	case "constitution":
		return Modifier(s.Constitution), nil
	case "intelligence":
		return Modifier(s.Intelligence), nil
	case "wisdom":
		return Modifier(s.Wisdom), nil
	case "charisma":
		return Modifier(s.Charisma), nil
	}
	return 0, errors.New("неизвестная характеристика")
}
