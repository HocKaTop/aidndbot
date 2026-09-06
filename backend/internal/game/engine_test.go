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
