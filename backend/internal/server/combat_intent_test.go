package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"strings"
	"testing"
)

func attackTestState() game.State {
	state := game.NewState(game.Settings{})
	state.Scene = &game.Scene{ID: "tavern", Location: "Таверна"}
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	state.NPCs = []game.NPC{{ID: "guard", Name: "Стражник", HP: 30, MaxHP: 30, ArmorClass: 12, Alive: true, Disposition: "neutral", Location: "Таверна"}}
	return state
}

func TestExplicitAttackNeverSubstitutesMissingTarget(t *testing.T) {
	for _, text := range []string{"Атакую дракона", "Атакую дракона мечом", "Атакую дракона огненным снарядом"} {
		state := attackTestState()
		results, handled, err := applyExplicitAttack(&state, 1, text)
		if !handled || err == nil || len(results) != 0 || state.NPCs[0].HP != 30 || state.Combat {
			t.Fatalf("%q attacked another NPC: results=%v, handled=%t, err=%v", text, results, handled, err)
		}
	}
	state := attackTestState()
	state.NPCs = append(state.NPCs, game.NPC{ID: "dragon", Name: "Дракон", HP: 40, Alive: true, Location: "Пещера"})
	if _, handled, err := applyExplicitAttack(&state, 1, "Атакую дракона"); !handled || err == nil || state.Combat {
		t.Fatal("remote named target caused local attack", handled, err)
	}
}

func TestNamedAttackAndClassAbilityUseTheirActualTarget(t *testing.T) {
	state := attackTestState()
	state.NPCs = append(state.NPCs, game.NPC{ID: "thief", Name: "Похититель", HP: 30, MaxHP: 30, ArmorClass: 12, Alive: true, Disposition: "neutral", Location: "Таверна"})
	state.NPCs = append(state.NPCs, game.NPC{ID: "watcher", Name: "Страж", HP: 30, MaxHP: 30, ArmorClass: 12, Alive: true, Disposition: "neutral", Location: "Таверна"})
	results, handled, err := applyExplicitAttack(&state, 1, "Я атакую стражника")
	if err != nil || !handled || len(results) != 3 || results[2].Type != "ATTACK" || results[2].Attack.Target != "Стражник" || state.NPCs[1].HP != 30 {
		t.Fatal("named attack hit wrong target", results, handled, err)
	}
	state = attackTestState()
	state.Characters[0].ClassID = "warrior"
	state.Characters[0].Resource = 2
	state.Characters[0].ResourceMax = 2
	results, handled, err = applyExplicitAttack(&state, 1, "Атакую стражника мощным ударом")
	if err != nil || !handled || len(results) != 3 || results[2].Type != "CLASS_ATTACK" || state.Characters[0].Resource != 1 || combatNarrative(results) == "" {
		t.Fatal("text ability differed from class action", results, handled, err)
	}
	state = attackTestState()
	state.Characters[0].ClassID = "warrior"
	state.Characters[0].ResourceMax = 2
	results, handled, err = applyExplicitAttack(&state, 1, "Использую мощный удар по стражнику")
	if err != nil || !handled || len(results) == 0 || results[len(results)-1].Type != "CLASS_ATTACK" {
		t.Fatal("named class action was not recognized", results, handled, err)
	}
	state = attackTestState()
	state.Characters[0].ClassID = "mage"
	results, handled, err = applyExplicitAttack(&state, 1, "Атакую огненным снарядом")
	if err != nil || !handled || len(results) != 3 || results[2].Type != "CLASS_ATTACK" || !strings.Contains(combatNarrative(results), "firebolt") {
		t.Fatal("spell against sole NPC did not use mage ability", results, handled, err)
	}
}

func TestConditionalAndComplexAttackRequirePlanning(t *testing.T) {
	for _, text := range []string{"Атакую стражника, если он нападёт", "Атакую стражника заклинанием", "Атакую стражника огненным шаром"} {
		state := attackTestState()
		_, handled, err := applyExplicitAttack(&state, 1, text)
		if handled || err != nil || state.Combat || state.NPCs[0].HP != 30 {
			t.Fatal("complex attack bypassed planning", text, handled, err)
		}
		actions := []game.Action{{Type: "SET_DISPOSITION", Target: "guard", Status: "hostile"}, {Type: "START_COMBAT"}, {Type: "ATTACK", Target: "guard"}}
		if err := validateAttackPlan(&state, text, actions); err == nil {
			t.Fatal("plan replaced conditional or spell attack with ordinary damage", text)
		}
	}
	state := attackTestState()
	state.NPCs = append(state.NPCs, game.NPC{ID: "thief", Name: "Похититель", HP: 30, Alive: true, Disposition: "neutral", Location: "Таверна"})
	if err := validateAttackPlan(&state, "Атакую стражника мечом", []game.Action{{Type: "ATTACK", Target: "thief"}}); err == nil {
		t.Fatal("AI plan changed explicitly named target")
	}
	if _, handled, err := applyExplicitAttack(&state, 1, "Атакую стражника, если похититель нападёт"); handled || err != nil {
		t.Fatal("NPC in condition was mistaken for attack target", handled, err)
	}
}

func TestConditionalAttackPlanIsRepairedBeforeCombat(t *testing.T) {
	state := attackTestState()
	calls := 0
	s := &Server{AI: openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		calls++
		if calls == 1 {
			return ai.Output{Narrative: "Стражник ранен.", Actions: []game.Action{{Type: "SET_DISPOSITION", Target: "guard", Status: "hostile"}, {Type: "START_COMBAT"}, {Type: "ATTACK", Target: "guard"}}}, nil
		}
		if in.Correction == nil || !strings.Contains(in.Correction.Reason, "условная атака") {
			t.Fatal("missing correction for premature attack", in.Correction)
		}
		return ai.Output{Narrative: "Ты ждёшь, нападёт ли стражник.", Actions: []game.Action{}}, nil
	})}
	out, err := s.planTurn(context.Background(), "room", ai.Input{State: state, PlayerID: 1, Text: "Атакую стражника, если он нападёт"})
	if err != nil || calls != 2 || len(out.Actions) != 0 || state.Combat || state.NPCs[0].HP != 30 {
		t.Fatal("conditional attack changed world", out, calls, err)
	}
}
