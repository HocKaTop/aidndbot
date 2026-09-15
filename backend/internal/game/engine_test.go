package game

import "testing"

func TestDice(t *testing.T) {
	for _, s := range []string{"d20", "d6", "2d6", "2d6+3", "1d20+5", "1d8-1"} {
		d, e := ParseDice(s)
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 100; i++ {
			r, e := RollDice(s)
			if e != nil || r.Total < d.Count+d.Modifier || r.Total > d.Count*d.Sides+d.Modifier {
				t.Fatal(r, e)
			}
		}
	}
	for _, s := range []string{"", "0d6", "d1", "21d6", "d1001", "1d6+101", "d6;rm", "-1d6", "999999999d6"} {
		if _, e := ParseDice(s); e == nil {
			t.Fatal(s)
		}
	}
}
func TestCombat(t *testing.T) {
	if Hits(1, 100, 1) || !Hits(20, 0, 100) || !Hits(10, 3, 13) || Hits(9, 3, 13) {
		t.Fatal("hit rules")
	}
	if ApplyDamage(3, 8) != 0 || ApplyDamage(3, -1) != 3 || Modifier(9) != -1 {
		t.Fatal("bounds")
	}
}
func TestActionValidation(t *testing.T) {
	s := NewState(Settings{})
	s.Characters = append(s.Characters, NewCharacter(1, "A", "human", "warrior"))
	for _, a := range []Action{{Type: "SET_HP"}, {Type: "ATTACK", Target: "missing"}, {Type: "SKILL_CHECK", Skill: "strength", DC: 100}, {Type: "REMOVE_ITEM", Target: "missing"}, {Type: "START_COMBAT"}} {
		if ValidateAction(&s, 1, a) == nil {
			t.Fatal(a)
		}
	}
	if _, e := Apply(&s, 1, Action{Type: "CREATE_NPC", Name: "Goblin", Status: "hostile"}); e != nil {
		t.Fatal(e)
	}
	if _, e := Apply(&s, 1, Action{Type: "START_COMBAT"}); e != nil {
		t.Fatal(e)
	}
	if ValidateAction(&s, 1, Action{Type: "MOVE_SCENE", Name: "elsewhere"}) == nil {
		t.Fatal("escaped combat")
	}
}

func TestSceneAndInventoryRules(t *testing.T) {
	s := NewState(Settings{})
	s.Characters = append(s.Characters, NewCharacter(1, "Hero", "Human", "Warrior"))
	s.Scene = &Scene{Location: "tavern"}
	s.NPCs = []NPC{{ID: "remote", HP: 12, Alive: true, Disposition: "hostile", Location: "forest"}}
	if ValidateAction(&s, 1, Action{Type: "START_COMBAT"}) == nil {
		t.Fatal("remote enemy started combat")
	}
	s.Combat = true
	if ValidateAction(&s, 1, Action{Type: "ATTACK", Target: "remote"}) == nil {
		t.Fatal("remote attack accepted")
	}
	results, e := Retaliate(&s, 1)
	if e != nil || len(results) != 0 || s.Characters[0].HP != 20 {
		t.Fatal("remote enemy retaliated", results, e)
	}
	potion := s.Characters[0].Inventory[0].ID
	if _, e = UseItem(&s, 1, potion); e == nil || s.Characters[0].Inventory[0].Quantity != 2 {
		t.Fatal("full health consumed potion")
	}
	s.Characters[0].HP = 19
	if _, e = UseItem(&s, 1, potion); e != nil || s.Characters[0].HP != 20 || s.Characters[0].Inventory[0].Quantity != 1 {
		t.Fatal("healing boundaries", e)
	}
	s.Characters[0].Inventory[0].Quantity = 2
	if _, e = Apply(&s, 1, Action{Type: "REMOVE_ITEM", Target: potion}); e != nil || s.Characters[0].Inventory[0].Quantity != 1 {
		t.Fatal("removed entire stack", e)
	}
	s.Quests = []Quest{{ID: "done", Status: "COMPLETED"}}
	if ValidateAction(&s, 1, Action{Type: "UPDATE_QUEST", Target: "done", Status: "FAILED"}) == nil {
		t.Fatal("terminal quest changed")
	}
}
func TestSkillCheckConsequences(t *testing.T) {
	s := NewState(Settings{})
	s.Characters = append(s.Characters, NewCharacter(1, "Hero", "Human", "Warrior"))
	actions := []Action{{Type: "SKILL_CHECK", Skill: "strength", DC: 25}, {Type: "ADD_ITEM", Name: "Treasure"}}
	out, e := ApplyActions(&s, 1, actions)
	if e != nil || out[0].Success == nil || *out[0].Success || out[1].Type != "ACTION_SKIPPED" || len(s.Characters[0].Inventory) != 2 {
		t.Fatal("failed check consequences", out, e)
	}
	s.Characters[0].Stats.Strength = 60
	if out, e = ApplyActions(&s, 1, actions); e != nil || !*out[0].Success || len(s.Characters[0].Inventory) != 3 {
		t.Fatal("successful check consequences", e)
	}
	if _, e = ApplyActions(&s, 1, []Action{actions[1], actions[0]}); e == nil {
		t.Fatal("reward before check")
	}
	if _, e = ApplyActions(&s, 1, []Action{actions[0], {Type: "DICE_ROLL", Name: "d20"}}); e == nil {
		t.Fatal("multiple checks accepted")
	}
}
