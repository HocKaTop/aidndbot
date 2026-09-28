package game

import "testing"

func TestNewClassCharacterTemplatesAndLegacy(t *testing.T) {
	for _, tc := range []struct {
		name, id, weapon string
		hp, ac, resource int
	}{
		{"Воин", "warrior", "Меч", 24, 15, 2},
		{"Плут", "rogue", "Кинжал", 18, 14, 2},
		{"Маг", "mage", "Посох", 16, 12, 3},
	} {
		h, err := NewClassCharacter(1, "Hero", "Человек", tc.name)
		if err != nil || h.ClassID != tc.id || h.MaxHP != tc.hp || h.HP != tc.hp || h.ArmorClass != tc.ac || h.ResourceMax != tc.resource || h.Resource != tc.resource || h.Inventory[1].Name != tc.weapon {
			t.Fatal(tc, h, err)
		}
	}
	if _, err := NewClassCharacter(1, "Hero", "Человек", "Некромант"); err == nil {
		t.Fatal("unknown class accepted")
	}
	old := NewCharacter(1, "Old", "Человек", "Воин")
	if old.ClassID != "" || old.HP != 20 || old.ArmorClass != 13 {
		t.Fatal("legacy character changed", old)
	}
}

func TestClassAbilitiesAndResourceReset(t *testing.T) {
	for _, tc := range []struct {
		class, ability string
		cost           int
	}{
		{"Воин", "power_strike", 1},
		{"Плут", "precise_strike", 1},
		{"Маг", "firebolt", 0},
	} {
		hero, err := NewClassCharacter(1, "Hero", "Человек", tc.class)
		if err != nil {
			t.Fatal(err)
		}
		s := NewState(Settings{})
		s.Characters = []Character{hero}
		s.Scene = &Scene{Location: "mill"}
		s.NPCs = []NPC{{ID: "enemy", Name: "Enemy", HP: 100, MaxHP: 100, ArmorClass: 12, Alive: true, Disposition: "neutral", Location: "mill"}}
		s.Hero(1).Resource = 0 // Starting a new fight must restore the class resource.
		results, err := UseAbility(&s, 1, tc.ability, "enemy")
		if err != nil || len(results) != 3 || results[1].Type != "START_COMBAT" || results[2].Attack == nil || s.Hero(1).Resource != s.Hero(1).ResourceMax-tc.cost {
			t.Fatal("opening class ability failed", tc, results, err)
		}
		if _, err := UseAbility(&s, 1, "other_class_ability", "enemy"); err == nil {
			t.Fatal("invalid ability accepted", tc)
		}
	}
	hero, _ := NewClassCharacter(1, "Hero", "Человек", "Воин")
	s := NewState(Settings{})
	s.Characters = []Character{hero}
	s.Scene = &Scene{Location: "mill"}
	s.NPCs = []NPC{{ID: "enemy", Name: "Enemy", HP: 100, MaxHP: 100, ArmorClass: 12, Alive: true, Disposition: "hostile", Location: "mill"}}
	s.Combat = true
	for i := 0; i < 2; i++ {
		results, err := UseAbility(&s, 1, "guard", "")
		if err != nil || len(results) != 1 || results[0].ArmorBonus != 4 {
			t.Fatal("guard failed", results, err)
		}
	}
	if _, err := UseAbility(&s, 1, "guard", ""); err == nil || s.Hero(1).Resource != 0 {
		t.Fatal("spent guard resource reused", err)
	}
}
