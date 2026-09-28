package game

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"
)

func TestDiceActionNotation(t *testing.T) {
	for _, value := range []string{"d20", "d6", "1d20", "2d6+3", "1d8-1", "d2", "20d1000-100", "01d0006+003", "d0002-000"} {
		if err := ValidateAction(&State{}, 1, Action{Type: "DICE_ROLL", Name: value}); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"прикосновение к занавесе", "проверка d20", "бросок d20", "проверка удачи", "roll d20", "", "d20\n", " d20", "D20", "0d6", "21d6", "d1", "d1001", "d6+101"} {
		if err := ValidateAction(&State{}, 1, Action{Type: "DICE_ROLL", Name: value}); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}

func TestDicePatternPreservesPreviousParserLanguage(t *testing.T) {
	// Independent oracle: the original syntax plus numeric validation. Cover
	// every permitted number and padding width, including just-outside values.
	oldSyntax := regexp.MustCompile(`^(\d{0,2})d(\d{1,4})([+-]\d{1,3})?$`)
	check := func(value string) {
		t.Helper()
		want := false
		if m := oldSyntax.FindStringSubmatch(value); m != nil {
			count := 1
			if m[1] != "" {
				count, _ = strconv.Atoi(m[1])
			}
			sides, _ := strconv.Atoi(m[2])
			modifier := 0
			if m[3] != "" {
				modifier, _ = strconv.Atoi(m[3])
			}
			want = count >= 1 && count <= 20 && sides >= 2 && sides <= 1000 && modifier >= -100 && modifier <= 100
		}
		_, err := ParseDice(value)
		if (err == nil) != want {
			t.Fatalf("language changed for %q: %v", value, err)
		}
	}
	for width := 1; width <= 5; width++ {
		for n := 0; n <= 1100; n++ {
			check(fmt.Sprintf("01d%0*d-003", width, n))
			if n <= 101 {
				check(fmt.Sprintf("%0*dd6+000", width, n))
				check(fmt.Sprintf("d20+%0*d", width, n))
				check(fmt.Sprintf("d20-%0*d", width, n))
			}
		}
	}
	check("d20")
}

func TestDiceCannotDuplicateAttackOrCheck(t *testing.T) {
	s := NewState(Settings{})
	s.Characters = []Character{NewCharacter(1, "Hero", "", "")}
	s.Combat = true
	s.NPCs = []NPC{{ID: "enemy", Alive: true, Disposition: "hostile"}}
	for _, mechanical := range []Action{{Type: "ATTACK", Target: "enemy"}, {Type: "SKILL_CHECK", Skill: "wisdom", DC: 12}, {Type: "DICE_ROLL", Name: "d6"}} {
		if err := ValidateActions(&s, 1, []Action{mechanical, {Type: "DICE_ROLL", Name: "d20"}}); err == nil {
			t.Fatal("accepted two mechanical actions", mechanical)
		}
	}
}
