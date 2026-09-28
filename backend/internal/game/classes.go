package game

import (
	"errors"
	"github.com/google/uuid"
	"strings"
)

func ClassID(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "воин", "warrior":
		return "warrior"
	case "плут", "разбойник", "rogue":
		return "rogue"
	case "маг", "mage":
		return "mage"
	default:
		return ""
	}
}

// Existing characters keep their saved values. This template is applied only
// when creating a new character or deliberately changing its class in a lobby.
func NewClassCharacter(user int64, name, race, class string) (Character, error) {
	id := ClassID(class)
	if id == "" {
		return Character{}, errors.New("выбери класс: Воин, Плут или Маг")
	}
	hero := NewCharacter(user, name, race, class)
	hero.ClassID = id
	switch id {
	case "warrior":
		hero.HP, hero.MaxHP, hero.ArmorClass = 24, 24, 15
		hero.Stats = Stats{16, 12, 14, 10, 10, 10}
		hero.Resource, hero.ResourceMax = 2, 2
	case "rogue":
		hero.HP, hero.MaxHP, hero.ArmorClass = 18, 18, 14
		hero.Stats = Stats{10, 16, 12, 12, 12, 10}
		hero.Resource, hero.ResourceMax = 2, 2
		hero.Inventory[1] = Item{uuid.NewString(), "Кинжал", "Урон 1d6+3", 1, "WEAPON"}
	case "mage":
		hero.HP, hero.MaxHP, hero.ArmorClass = 16, 16, 12
		hero.Stats = Stats{8, 12, 10, 16, 12, 10}
		hero.Resource, hero.ResourceMax = 3, 3
		hero.Inventory[1] = Item{uuid.NewString(), "Посох", "Урон 1d6-1", 1, "WEAPON"}
	}
	return hero, nil
}
